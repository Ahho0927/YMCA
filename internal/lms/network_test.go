package lms

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTrustedDefaultPorts(t *testing.T) {
	c := New(context.Background(), nil)
	for _, test := range []struct {
		url     string
		trusted bool
	}{
		{"https://lms.ynu.ac.jp/lms/", true},
		{"https://lms.ynu.ac.jp:443/lms/", true},
		{"https://gakunin-idp.ynu.ac.jp:443/openam/XUI/", true},
		{"https://GAKUNIN-IDP.YNU.AC.JP:443/openam/XUI/", true},
		{"https://gakunin-idp.ynu.ac.jp:444/openam/", false},
		{"https://lms.ynu.ac.jp:8443/lms/", false},
		{"http://gakunin-idp.ynu.ac.jp:443/openam/", false},
		{"https://gakunin-idp.ynu.ac.jp.example.com/openam/", false},
		{"https://user:secret@gakunin-idp.ynu.ac.jp:443/openam/", false},
	} {
		if err := c.checkURL(mustURL(test.url)); (err == nil) != test.trusted {
			t.Fatalf("origin check trusted=%t, wanted %t", err == nil, test.trusted)
		}
	}
	if !c.isLMSHomeURL(mustURL("https://lms.ynu.ac.jp:443/lms/homeHoml/;SID=fixture")) {
		t.Fatal("dashboard with explicit HTTPS port was rejected")
	}
	if !sameOrigin(mustURL("http://localhost"), mustURL("http://localhost:80")) {
		t.Fatal("default HTTP ports were not normalized")
	}
}

func TestAuthenticationWithExplicitHTTPSPorts(t *testing.T) {
	c := New(context.Background(), nil)
	dashboard := fixture(t)
	authCalls := 0
	c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response := &http.Response{StatusCode: 200, Header: make(http.Header), Request: r}
		body := ""
		path := strings.Split(r.URL.Path, ";")[0]
		switch path {
		case "/lms/lginLgir/":
			body = `<form id=gakuninLoginForm action='https://lms.ynu.ac.jp:443/lms/gakuninForward;SID=fixture'></form>`
		case "/lms/gakuninForward":
			response.StatusCode = 302
			response.Header.Set("Location", "https://lms.ynu.ac.jp:443/eduapi/gknsso/CoursePower/")
		case "/eduapi/gknsso/CoursePower/":
			response.StatusCode = 302
			response.Header.Set("Location", "https://gakunin-idp.ynu.ac.jp:443/openam/XUI/#login/&realm=/usr&goto=%2Fopenam%2FSSORedirect%2Ftest")
		case "/openam/XUI/":
			body = "OpenAM"
		case "/openam/json/authenticate":
			authCalls++
			if authCalls == 1 {
				body = `{"authId":"fixture","stage":"OpenLDAP1","callbacks":[{"type":"NameCallback","input":[{"name":"IDToken1","value":""}]},{"type":"PasswordCallback","input":[{"name":"IDToken2","value":""}]}]}`
			} else {
				body = `{"tokenId":"fixture","successUrl":"https://gakunin-idp.ynu.ac.jp:443/openam/SSORedirect/test"}`
			}
		case "/openam/json/users":
			body = `{"id":"fixture"}`
		case "/openam/SSORedirect/test":
			body = `<form action='https://lms.ynu.ac.jp:443/lms/saml'><input name=SAMLResponse value=fixture></form>`
		case "/lms/saml":
			response.StatusCode = 302
			response.Header.Set("Location", "https://lms.ynu.ac.jp:443/lms/homeHoml/;SID=fixture")
		case "/lms/homeHoml/":
			body = dashboard
		case "/lms/homeHoml/doIndex":
			response.StatusCode = 302
			response.Header.Set("Location", "https://lms.ynu.ac.jp:443/lms/homeHoml/;SID=fixture")
		default:
			t.Fatalf("unexpected request path: %s", path)
		}
		response.Body = io.NopCloser(strings.NewReader(body))
		return response, nil
	})
	d, err := c.Login("fixture", "fixture", func() (string, error) {
		t.Fatal("unexpected second factor")
		return "", nil
	})
	if err != nil || d == nil || !d.Verified || authCalls != 2 {
		t.Fatalf("explicit-port login failed: %v", err)
	}
}

func TestRequestFailureDoesNotExposeURLs(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://gakunin-idp.ynu.ac.jp/openam/json/authenticate?authId=private-query", nil)
	for _, test := range []struct {
		cause error
		want  string
	}{
		{&net.DNSError{Name: "gakunin-idp.ynu.ac.jp", Err: "private-server-diagnostic"}, "DNS lookup failed"},
		{context.DeadlineExceeded, "request timed out"},
		{context.Canceled, "request canceled"},
		{errUntrustedTarget, "redirect blocked"},
		{errRedirectLimit, "too many redirects"},
		{io.ErrUnexpectedEOF, "connection closed"},
		{errors.New("private-server-diagnostic"), "network request failed"},
	} {
		wrapped := &url.Error{Op: "Post", URL: "https://lms.ynu.ac.jp/path;SID=private-session?SAMLResponse=private-saml", Err: test.cause}
		err := requestFailure(req, wrapped)
		if !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "SAMLResponse") {
			t.Fatalf("unsafe or incomplete diagnostic: %s", err)
		}
		if !errors.Is(err, test.cause) {
			t.Fatal("error cause was lost")
		}
	}
}

// Opt-in, credential-free smoke check of the university's real redirect chain.
// Normal tests and CI never contact the university.
func TestPublicLoginHandoff(t *testing.T) {
	if os.Getenv("YNU_LIVE_PREFLIGHT") != "1" {
		t.Skip("set YNU_LIVE_PREFLIGHT=1 to check public login redirects without credentials")
	}
	c := New(context.Background(), nil)
	initial, err := c.getInitialLMSState()
	if err != nil {
		t.Fatalf("public LMS login page: %v", err)
	}
	xui, err := c.startGakuninForward(initial)
	if err != nil {
		t.Fatalf("public OpenAM handoff: %v", err)
	}
	u := mustURL(xui.xuiURL)
	t.Logf("public login handoff reached %s at HTTPS port %s; no credentials submitted", u.Hostname(), u.Port())
}
