package lms

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"ymca/internal/state"
)

func selectorClient(t *testing.T, dashboard *string, codes map[string]string, failed map[string]bool) (*Client, map[string]int) {
	t.Helper()
	calls := map[string]int{}
	c := New(context.Background(), &state.Session{SID: "fixture"})
	c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := *dashboard
		if strings.HasPrefix(r.URL.Path, "/lms/homeHoml/linkKougi") {
			r.ParseForm()
			id := r.Form.Get("kougiId")
			calls[id]++
			if failed[id] {
				return nil, errors.New("temporary network failure")
			}
			code, ok := codes[id]
			if !ok {
				t.Fatalf("unexpected course lookup %s", id)
			}
			body = `<a class="logoutButton">Logout</a><div class="courseName">Example</div>`
			if code != "" {
				body += fmt.Sprintf(`<div class="breadCrumbBar"><p>Example[%s]</p></div>`, code)
			}
		} else if !strings.HasPrefix(r.URL.Path, "/lms/homeHoml/doIndex") {
			t.Fatalf("lookup must not fetch lessons, reports or files: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	return c, calls
}

func TestCourseCodeResolutionAndCache(t *testing.T) {
	dashboard := fixture(t)
	codes := map[string]string{"KOG-a": "801105Z", "KOG-b": "123456A", "KOG-c": "123456B", "KOG-d": ""}
	c, calls := selectorClient(t, &dashboard, codes, nil)
	id, err := c.ResolveCourse("801105z", nil)
	if err != nil || id != "KOG-a" {
		t.Fatalf("%s: %v", id, err)
	}
	if len(calls) != 4 {
		t.Fatal("did not check all potential duplicate codes", calls)
	}
	for k := range calls {
		calls[k] = 0
	}
	id, err = c.ResolveCourse("801105Z", nil)
	if err != nil || id != "KOG-a" {
		t.Fatalf("cached lookup: %s, %v", id, err)
	}
	for _, n := range calls {
		if n != 0 {
			t.Fatal("cached lookup fetched details", calls)
		}
	}
	// A different selected year must not reuse the previous year's mapping.
	dashboard = strings.ReplaceAll(dashboard, `value="2025" selected`, `value="2027" selected`)
	codes["KOG-a"] = "765432Z"
	id, err = c.ResolveCourse("765432Z", nil)
	if err != nil || id != "KOG-a" || calls["KOG-a"] != 1 {
		t.Fatalf("year isolation: %s, %v, %v", id, err, calls)
	}
}

func TestDuplicateAndIncompleteCodeMappings(t *testing.T) {
	dashboard := fixture(t)
	codes := map[string]string{"KOG-a": "801105Z", "KOG-b": "801105Z", "KOG-c": "123456B", "KOG-d": ""}
	c, _ := selectorClient(t, &dashboard, codes, nil)
	if _, err := c.ResolveCourse("801105Z", nil); err == nil || !strings.Contains(err.Error(), "KOG-a") || !strings.Contains(err.Error(), "KOG-b") {
		t.Fatal("ambiguous course was selected", err)
	}
	failed := map[string]bool{"KOG-b": true}
	c, calls := selectorClient(t, &dashboard, codes, failed)
	if _, err := c.ResolveCourse("801105Z", nil); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatal("partial scan accepted", err)
	}
	// Successful entries survive a partial failure; retry only the failed entry.
	failed["KOG-b"] = false
	codes["KOG-b"] = "123456A"
	id, err := c.ResolveCourse("801105Z", nil)
	if err != nil || id != "KOG-a" || calls["KOG-a"] != 1 || calls["KOG-b"] != 2 {
		t.Fatalf("partial retry: %s, %v, %v", id, err, calls)
	}
	// Cached IDs not in the current dashboard must not participate in lookup.
	c.CourseCodes["2025"]["KOG-removed"] = "801105Z"
	if id, err = c.ResolveCourse("801105Z", nil); err != nil || id != "KOG-a" {
		t.Fatalf("stale membership: %s, %v", id, err)
	}
	if _, err = c.ResolveCourse("NOTHERE", nil); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatal(err)
	}
}

func TestDirectCourseIDDoesNotScan(t *testing.T) {
	c := New(context.Background(), nil)
	c.httpClient.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		t.Fatal("direct ID resolution accessed network")
		return nil, nil
	})
	if id, err := c.ResolveCourse("KOG0000000163774", nil); err != nil || id != "KOG0000000163774" {
		t.Fatal(id, err)
	}
	for _, bad := range []string{"my-physics", "801105Z/", "", "../a", "12345678"} {
		if ValidCourseSelector(bad) {
			t.Fatalf("invalid selector accepted: %q", bad)
		}
	}
}

func TestCodeScanStopsOnExpiredSession(t *testing.T) {
	dashboard := fixture(t)
	c := New(context.Background(), &state.Session{SID: "fixture"})
	lookups := 0
	c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := dashboard
		if strings.Contains(r.URL.Path, "linkKougi") {
			lookups++
			body = `<form id="gakuninLoginForm"></form>`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	if _, err := c.ResolveCourse("801105Z", nil); !errors.Is(err, ErrAuth) || lookups != 1 {
		t.Fatalf("%v, lookups=%d", err, lookups)
	}
}

func TestCodeScanMalformedEntryIsNotCached(t *testing.T) {
	dashboard := fixture(t)
	c := New(context.Background(), &state.Session{SID: "fixture"})
	c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := dashboard
		if strings.Contains(r.URL.Path, "linkKougi") {
			body = `<a class="logoutButton">Logout</a><div>maintenance</div>`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	if _, err := c.ResolveCourse("801105Z", nil); !errors.Is(err, ErrStructure) {
		t.Fatal(err)
	}
	if len(c.CourseCodes["2025"]) != 0 {
		t.Fatal("malformed entries were cached")
	}
}

func TestSevenCharacterCodeBeginningWithKOG(t *testing.T) {
	dashboard := fixture(t)
	codes := map[string]string{"KOG-a": "KOG1234", "KOG-b": "123456A", "KOG-c": "123456B", "KOG-d": ""}
	c, _ := selectorClient(t, &dashboard, codes, nil)
	if id, err := c.ResolveCourse("KOG1234", nil); err != nil || id != "KOG-a" {
		t.Fatalf("seven-character code treated as internal ID: %s %v", id, err)
	}
}
