package lms

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/net/html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDownloadFilenameEncodingAndExtension(t *testing.T) {
	japanese := "都市基盤構造力学2026　第2回（静定梁の解析）.pdf"
	longJapanese := strings.Repeat("都市基盤構造力学", 20) + ".pdf"
	longASCII := strings.Repeat("lecture-notes-", 20) + ".pptx"
	for _, tc := range []struct {
		name, header, title, want, extension string
	}{
		{"legacy UTF-8", `attachment; filename="` + url.PathEscape(japanese) + `"`, "", japanese, ".pdf"},
		{"RFC 5987", `attachment; filename="fallback.pdf"; filename*=UTF-8''` + url.PathEscape(japanese), "", japanese, ".pdf"},
		{"RFC 2231 continuation", `attachment; filename*0*=UTF-8''%E8%B3%87; filename*1*=%E6%96%99.pdf`, "", "資料.pdf", ".pdf"},
		{"extended decoded once", `attachment; filename*=UTF-8''literal%2520%2Bname.pdf`, "", "literal%20+name.pdf", ".pdf"},
		{"literal plus", `attachment; filename="lecture+notes%20%2B.pdf"`, "", "lecture+notes +.pdf", ".pdf"},
		{"quoted parameter text", `attachment; filename="lecture;filename*=notes%20.pdf"`, "", "lecture;filename_=notes .pdf", ".pdf"},
		{"invalid escape", `attachment; filename="100%ZZ.pdf"`, "", "100%ZZ.pdf", ".pdf"},
		{"encoded traversal", `attachment; filename="..%2F..%2F%E8%B3%87%E6%96%99.pdf"`, "", "資料.pdf", ".pdf"},
		{"missing header", "", japanese, japanese, ".pdf"},
		{"long encoded UTF-8", `attachment; filename="` + url.PathEscape(longJapanese) + `"`, "", "", ".pdf"},
		{"long ASCII", `attachment; filename="` + longASCII + `"`, "", "", ".pptx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.header != "" {
					w.Header().Set("Content-Disposition", tc.header)
				}
				w.Header().Set("Content-Type", "application/octet-stream")
				fmt.Fprint(w, "%PDF-1.4\nfixture\x00\xff")
			})
			m := Material{Title: tc.title, request: navigation{method: "GET", url: c.lmsOrigin + "/downloadFile"}}
			dir := t.TempDir()
			path, err := c.Download(m, dir)
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Base(path)
			if tc.want != "" && name != tc.want {
				t.Fatalf("filename = %q, want %q", name, tc.want)
			}
			if !utf8.ValidString(name) || len(name) > 220 || filepath.Ext(name) != tc.extension {
				t.Fatalf("invalid or extensionless filename: %q (%d bytes)", name, len(name))
			}
			second, err := c.Download(m, dir)
			wantSecond := strings.TrimSuffix(name, tc.extension) + " (1)" + tc.extension
			if err != nil || filepath.Base(second) != wantSecond {
				t.Fatalf("duplicate filename = %q, want %q: %v", second, wantSecond, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "%PDF-1.4\nfixture\x00\xff" {
				t.Fatal("binary download changed", err)
			}
		})
	}
}

func TestSafeFilenameHandlesInvalidUTF8(t *testing.T) {
	name := safeFilename("\xff\xfe資料.pdf")
	if !utf8.ValidString(name) || name != "_資料.pdf" {
		t.Fatalf("invalid filename: %q", name)
	}
}

func TestDownloadAndNames(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="../../example.pdf"`)
		w.Header().Set("Content-Type", "application/pdf")
		fmt.Fprint(w, "%PDF-1.4\nfixture\x00\xff")
	})
	m := Material{request: navigation{method: "GET", url: c.lmsOrigin + "/download"}}
	dir := t.TempDir()
	first, e := c.Download(m, dir)
	if e != nil {
		t.Fatal(e)
	}
	second, e := c.Download(m, dir)
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Base(first) != "example.pdf" || filepath.Base(second) != "example (1).pdf" {
		t.Fatalf("paths: %s %s", first, second)
	}
	b, e := os.ReadFile(first)
	if e != nil || string(b) != "%PDF-1.4\nfixture\x00\xff" {
		t.Fatal("binary file changed", e)
	}
	for _, name := range []string{`..\..\CON.txt`, `../../report.pdf`, "\x1bname\n.pdf", "..."} {
		safe := safeFilename(name)
		if strings.ContainsAny(safe, "/\\\n\x1b") || safe == "" {
			t.Fatal(safe)
		}
	}
}
func TestDownloadRejectsLoginAndIncomplete(t *testing.T) {
	for _, mode := range []string{"login", "html", "short"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/octet-stream")
				switch mode {
				case "login":
					fmt.Fprint(w, `<form id="gakuninLoginForm">Login</form>`)
				case "html":
					fmt.Fprint(w, `<!doctype html><html>Error</html>`)
				case "short":
					w.Header().Set("Content-Length", "100")
					fmt.Fprint(w, "short")
				}
			})
			dir := t.TempDir()
			_, err := c.Download(Material{request: navigation{method: "GET", url: c.lmsOrigin + "/download"}}, dir)
			if err == nil {
				t.Fatal("bad download saved")
			}
			if mode == "login" && !errors.Is(err, ErrAuth) {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatal("partial file retained")
			}
		})
	}
}
func TestDownloadCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := New(ctx, nil)
	_, err := c.Download(Material{request: navigation{method: "GET", url: "https://lms.ynu.ac.jp/download"}}, t.TempDir())
	if err == nil {
		t.Fatal("canceled download succeeded")
	}
}
func TestNavigationDoesNotExecuteSubmission(t *testing.T) {
	p := Page{URL: "https://lms.ynu.ac.jp/lms/home/", Body: fixture(t)}
	a := first(document(p.Body), func(n *html.Node) bool { return strings.Contains(attr(n, "onclick"), "formSubmit") })
	nav, err := navigationFor(p, a)
	if err != nil {
		t.Fatal(err)
	}
	if nav.values.Get("kougiId") != "KOG-c" || !strings.Contains(nav.url, "linkKougi") {
		t.Fatal(nav)
	}
	for _, href := range []string{"/lms/attendance/", "/lms/report/submit", "/lms/logout/"} {
		p.Body = `<a href="` + href + `">Action</a>`
		a = first(document(p.Body), func(n *html.Node) bool { return n.Data == "a" })
		if _, e := navigationFor(p, a); e == nil {
			t.Fatal("mutation accepted", href)
		}
	}
}

func TestMaterialIDDoesNotDependOnSession(t *testing.T) {
	a := navigation{method: "GET", url: "https://lms.ynu.ac.jp/file/download;SID=old?fileId=42&token=old"}
	b := navigation{method: "GET", url: "https://lms.ynu.ac.jp/file/download;SID=new?fileId=42&token=new"}
	if stableResourceID(a) != stableResourceID(b) {
		t.Fatal("material ID changed with the session")
	}
}
