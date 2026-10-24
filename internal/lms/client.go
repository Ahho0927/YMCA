package lms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/publicsuffix"
	"ymca/internal/state"
)

var ErrAuth = errors.New("session is missing or expired; run 'ymca auth login'")
var ErrNotFound = errors.New("resource was not found")
var ErrStructure = errors.New("LMS page structure is not supported; capture this page for parser verification")

var errUntrustedTarget = errors.New("request or redirect points outside the trusted LMS and authentication origins")
var errRedirectLimit = errors.New("too many LMS redirects")

// cookieJar records original Set-Cookie attributes; cookiejar.Cookies loses them.
type cookieJar struct {
	jar   *cookiejar.Jar
	saved map[string]state.Cookie
}

func newJar(saved []state.Cookie) *cookieJar {
	j, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	out := &cookieJar{jar: j, saved: map[string]state.Cookie{}}
	for _, s := range saved {
		u, e := url.Parse(s.URL)
		if e == nil {
			c := s.Cookie
			out.SetCookies(u, []*http.Cookie{&c})
		}
	}
	return out
}
func (j *cookieJar) Cookies(u *url.URL) []*http.Cookie { return j.jar.Cookies(u) }
func (j *cookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.jar.SetCookies(u, cookies)
	for _, c := range cookies {
		v := *c
		if v.Path == "" || v.Path[0] != '/' {
			v.Path = path.Dir(u.Path)
			if v.Path == "." {
				v.Path = "/"
			}
		}
		host := strings.TrimPrefix(strings.ToLower(v.Domain), ".")
		if host == "" {
			host = u.Hostname()
		}
		if host != u.Hostname() && !strings.HasSuffix(u.Hostname(), "."+host) {
			continue
		}
		key := host + "\n" + v.Path + "\n" + v.Name
		if v.MaxAge < 0 || (!v.Expires.IsZero() && !v.Expires.After(time.Now())) {
			delete(j.saved, key)
			continue
		}
		if v.MaxAge > 0 {
			v.Expires = time.Now().Add(time.Duration(v.MaxAge) * time.Second)
			v.MaxAge = 0
		}
		origin := *u
		origin.Path = v.Path
		origin.RawQuery = ""
		origin.Fragment = ""
		j.saved[key] = state.Cookie{URL: origin.String(), Cookie: v}
	}
}
func (j *cookieJar) snapshot() []state.Cookie {
	out := []state.Cookie{}
	for _, v := range j.saved {
		if v.Cookie.Expires.IsZero() || v.Cookie.Expires.After(time.Now()) {
			out = append(out, v)
		}
	}
	return out
}

type Client struct {
	httpClient     *http.Client
	jar            *cookieJar
	ctx            context.Context
	sid            string
	activity       time.Time
	lang           string
	acceptLanguage string
	promptCode     func() (string, error)
	// Only tests override these origins; the CLI cannot send credentials to arbitrary hosts.
	lmsOrigin   string
	idpOrigin   string
	Capture     func(string, []byte) error
	CourseCodes map[string]map[string]string
}
type client = Client

func New(ctx context.Context, s *state.Session) *Client {
	var cookies []state.Cookie
	sid := ""
	if s != nil {
		cookies = s.Cookies
		sid = s.SID
	}
	jar := newJar(cookies)
	c := &Client{ctx: ctx, jar: jar, sid: sid, acceptLanguage: acceptLanguageFor(""), lmsOrigin: "https://lms.ynu.ac.jp", idpOrigin: "https://gakunin-idp.ynu.ac.jp"}
	c.httpClient = &http.Client{Jar: jar, Timeout: 45 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errRedirectLimit
		}
		return c.checkURL(req.URL)
	}}
	if s != nil {
		c.activity = s.LastActivity
	}
	return c
}
func (c *Client) checkURL(u *url.URL) error {
	for _, origin := range []string{c.lmsOrigin, c.idpOrigin} {
		v, _ := url.Parse(origin)
		if sameOrigin(u, v) && u.User == nil {
			return nil
		}
	}
	return errUntrustedTarget
}

// Explicit default ports are equivalent to omitted ports, but a different
// scheme or non-default port is a different origin and must stay blocked.
func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		switch u.Scheme {
		case "https":
			return "443"
		case "http":
			return "80"
		}
		return ""
	}
	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}
func (c *Client) resolve(raw string) string {
	raw = strings.Replace(raw, "https://lms.ynu.ac.jp", c.lmsOrigin, 1)
	return strings.Replace(raw, "https://gakunin-idp.ynu.ac.jp", c.idpOrigin, 1)
}
func (c *Client) Login(username, password string, code func() (string, error)) (*Dashboard, error) {
	c.promptCode = code
	initial, err := c.getInitialLMSState()
	if err != nil {
		return nil, fmt.Errorf("LMS login page: %w", err)
	}
	xui, err := c.startGakuninForward(initial)
	if err != nil {
		return nil, fmt.Errorf("LMS-to-OpenAM handoff: %w", err)
	}
	result, err := c.openAMAuthFlow(xui, username, password, "")
	if err != nil {
		return nil, fmt.Errorf("OpenAM authentication: %w", err)
	}
	if err = c.validateOpenAMSession(xui, result); err != nil {
		return nil, fmt.Errorf("OpenAM session validation: %w", err)
	}
	sid, err := c.followSAMLAndLMS(xui, result)
	if err != nil {
		return nil, fmt.Errorf("SAML handoff: %w", err)
	}
	c.sid = sid
	d, err := c.Dashboard()
	if err != nil {
		return nil, fmt.Errorf("LMS dashboard: %w", err)
	}
	return d, nil
}
func (c *Client) LastActivity() time.Time { return c.activity }
func (c *Client) Snapshot() *state.Session {
	return &state.Session{SID: c.sid, Cookies: c.jar.snapshot(), LastActivity: c.activity}
}
func (c *Client) Dashboard() (*Dashboard, error) {
	if c.sid == "" {
		return nil, ErrAuth
	}
	p, err := c.page("GET", c.lmsOrigin+"/lms/homeHoml/doIndex;SID="+url.PathEscape(c.sid), nil)
	if err != nil {
		return nil, err
	}
	d, err := ParseDashboard(p.Body)
	if d != nil {
		d.Verified = true
	}
	return d, err
}

type Page struct {
	URL  string
	Body string
}

func (c *Client) page(method, raw string, form url.Values) (Page, error) {
	return c.readHTML(method, raw, form, false)
}

// Fragments are fetched only through loadBlock calls discovered in an already
// authenticated page. They intentionally have no account header or logout form.
func (c *Client) readHTML(method, raw string, form url.Values, fragment bool) (Page, error) {
	resp, body, err := c.do(method, raw, nil, form, nil)
	if err != nil {
		return Page{}, err
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return Page{}, ErrAuth
	}
	if resp.StatusCode >= 400 {
		return Page{}, fmt.Errorf("LMS returned HTTP %d", resp.StatusCode)
	}
	u := resp.Request.URL
	if !sameOrigin(u, mustURL(c.lmsOrigin)) || strings.Contains(u.Path, "/lginLgir/") || byID(document(body), "gakuninLoginForm") != nil {
		return Page{}, ErrAuth
	}
	// Preserve an explicitly requested private capture even if the parser has
	// never seen this authenticated subpage. Login/IdP responses were excluded above.
	if c.Capture != nil {
		if err = c.Capture(u.Path, []byte(body)); err != nil {
			return Page{}, err
		}
	}
	if !authenticatedHTML(body) {
		lower := strings.ToLower(text(document(body)))
		for _, marker := range []string{"セッションがタイムアウト", "セッションが無効", "session has expired", "session timeout"} {
			if strings.Contains(lower, marker) {
				return Page{}, ErrAuth
			}
		}
		if !fragment {
			return Page{}, ErrStructure
		}
	}
	if sid := extractSID(u.String()); sid != "" {
		c.sid = sid
	}
	c.activity = time.Now().UTC()
	return Page{u.String(), body}, nil
}
func authenticatedHTML(s string) bool {
	d := document(s)
	if byID(d, "gakuninLoginForm") != nil {
		return false
	}
	return byID(d, "homeHomlForm") != nil || first(d, func(n *html.Node) bool {
		return class(n, "logoutButton") || strings.Contains(attr(n, "action"), "/lms/logout/")
	}) != nil
}
func mustURL(s string) *url.URL { u, _ := url.Parse(s); return u }
func (c *Client) do(method, raw string, headers map[string]string, form url.Values, jsonBody any) (*http.Response, string, error) {
	var body io.Reader
	if jsonBody != nil {
		b, err := json.Marshal(jsonBody)
		if err != nil {
			return nil, "", err
		}
		body = bytes.NewReader(b)
	} else if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(c.ctx, method, c.resolve(raw), body)
	if err != nil {
		return nil, "", errors.New("invalid LMS request")
	}
	if err = c.checkURL(req.URL); err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "YMCA/0.1 (YNU LMS CLI)")
	req.Header.Set("Accept-Language", c.acceptLanguage)
	if jsonBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, c.resolve(v))
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", requestFailure(req, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024+1))
	if err != nil {
		return nil, "", requestFailure(req, err)
	}
	if len(b) > 16*1024*1024 {
		return nil, "", errors.New("LMS HTML response exceeded 16 MiB")
	}
	return resp, string(b), nil
}
