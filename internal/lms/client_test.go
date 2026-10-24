package lms

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ymca/internal/state"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := New(context.Background(), &state.Session{SID: "fixture"})
	c.lmsOrigin = server.URL
	c.idpOrigin = server.URL
	return c
}
func TestDashboardRequestAndRestore(t *testing.T) {
	html := fixture(t)
	requests := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if !strings.HasPrefix(r.URL.Path, "/lms/homeHoml/doIndex;SID=") {
			t.Error(r.URL.Path)
		}
		http.SetCookie(w, &http.Cookie{Name: "scoped", Value: "value", Path: "/lms", HttpOnly: true})
		fmt.Fprint(w, html)
	})
	d, e := c.Dashboard()
	if e != nil || !d.Verified || requests != 1 {
		t.Fatalf("result: %v %v, requests %d", d, e, requests)
	}
	snapshot := c.Snapshot()
	restored := newJar(snapshot.Cookies)
	cookies := restored.Cookies(mustURL(c.lmsOrigin + "/lms/"))
	if len(cookies) != 1 || len(restored.Cookies(mustURL(c.lmsOrigin+"/other"))) != 0 {
		t.Fatal("cookie path was not preserved")
	}
}
func TestExpired200AndMaintenance(t *testing.T) {
	for _, v := range []struct {
		body string
		want error
	}{{`<form id=gakuninLoginForm></form>`, ErrAuth}, {`<html>Maintenance</html>`, ErrStructure}} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, v.body) })
		if _, e := c.Dashboard(); !errors.Is(e, v.want) {
			t.Fatalf("got %v want %v", e, v.want)
		}
	}
}
func TestCookiesAttributesAndExpiry(t *testing.T) {
	u := mustURL("https://lms.ynu.ac.jp/lms/page")
	j := newJar(nil)
	j.SetCookies(u, []*http.Cookie{{Name: "one", Value: "1", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 60}, {Name: "two", Value: "2", Path: "/", Domain: "lms.ynu.ac.jp"}})
	saved := j.snapshot()
	if len(saved) != 2 {
		t.Fatal(saved)
	}
	j = newJar(saved)
	for _, v := range j.snapshot() {
		if v.Cookie.Name == "one" && (!v.Cookie.Secure || !v.Cookie.HttpOnly || v.Cookie.Path != "/lms" || v.Cookie.Expires.Before(time.Now())) {
			t.Fatalf("attributes lost: %+v", v)
		}
	}
	j.SetCookies(u, []*http.Cookie{{Name: "one", Value: "", Path: "/lms", MaxAge: -1}})
	if len(j.snapshot()) != 1 {
		t.Fatal("deleted cookie retained")
	}
}
func TestFormsDOM(t *testing.T) {
	forms := collectForms(`<form id='x' action='/test?a=&amp;b=1'><input name='a' value='1'><input name='a' value='2'><input name='disabled' disabled value='x'><input type=checkbox name='c' value='off'><input type=checkbox name='c' value='on' checked><select name='s'><option value='1'>One</option><option value='2' selected>Two</option></select></form>`)
	f := findForm(forms, "id", "x")
	v := formValues(f)
	if len(v["a"]) != 2 || v.Get("c") != "on" || v.Get("s") != "2" || v.Has("disabled") {
		t.Fatal(v)
	}
}
func TestAuthCallbacksAndSAML(t *testing.T) {
	for _, twoFA := range []bool{false, true} {
		t.Run(fmt.Sprint(twoFA), func(t *testing.T) {
			step := 0
			codeCalls := 0
			dashboard := fixture(t)
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch strings.Split(r.URL.Path, ";")[0] {
				case "/lms/lginLgir/":
					fmt.Fprint(w, `<form id=gakuninLoginForm method=post action='/lms/gakuninForward;SID=initial'><input type=hidden name=lang value=ja></form>`)
				case "/lms/gakuninForward":
					http.Redirect(w, r, "/openam/XUI/#login/&realm=/usr&goto=%2FSSORedirect%2Ftest", http.StatusFound)
				case "/openam/XUI/":
					fmt.Fprint(w, "OpenAM")
				case "/openam/json/authenticate":
					step++
					switch step {
					case 1:
						fmt.Fprint(w, `{"authId":"fixture","stage":"OpenLDAP1","callbacks":[{"type":"NameCallback","input":[{"name":"IDToken1","value":""}]},{"type":"PasswordCallback","input":[{"name":"IDToken2","value":""}]}]}`)
					case 2:
						if twoFA {
							fmt.Fprint(w, `{"authId":"fixture","stage":"HOTP2","callbacks":[{"type":"PasswordCallback","input":[{"name":"IDToken1","value":""}]},{"type":"ConfirmationCallback","input":[{"name":"IDToken2","value":0}]}]}`)
						} else {
							fmt.Fprint(w, `{"tokenId":"fixture","successUrl":"/openam/SSORedirect/test"}`)
						}
					case 3:
						fmt.Fprint(w, `{"tokenId":"fixture","successUrl":"/openam/SSORedirect/test"}`)
					}
				case "/openam/json/users":
					fmt.Fprint(w, `{"id":"example"}`)
				case "/openam/SSORedirect/test":
					fmt.Fprint(w, `<form action='/lms/saml'><input name=SAMLResponse value=fixture></form>`)
				case "/lms/saml":
					r.ParseForm()
					if r.Form.Get("SAMLResponse") != "fixture" {
						t.Error("SAML response not submitted")
					}
					fmt.Fprint(w, `<form name=gknssoform action='/lms/lginLgir/gakuninLogin'><input name=bridge value=fixture></form>`)
				case "/lms/lginLgir/gakuninLogin":
					http.Redirect(w, r, "/lms/homeHoml/;SID=fixture", http.StatusFound)
				case "/lms/homeHoml/", "/lms/homeHoml/doIndex":
					fmt.Fprint(w, dashboard)
				default:
					t.Error("unexpected request", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			d, e := c.Login("student", "password", func() (string, error) { codeCalls++; return "123456", nil })
			if e != nil || d == nil {
				t.Fatal(e)
			}
			if (codeCalls == 1) != twoFA {
				t.Fatalf("code prompts %d", codeCalls)
			}
		})
	}
}
func TestUntrustedFormAndRedirect(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/secret", http.StatusFound)
	})
	if _, e := c.Dashboard(); e == nil {
		t.Fatal("untrusted redirect accepted")
	}
	if _, _, e := c.do("POST", "https://example.com/", nil, url.Values{"password": {"secret"}}, nil); e == nil {
		t.Fatal("untrusted credential target accepted")
	}
}
