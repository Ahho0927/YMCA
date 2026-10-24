package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ymca/internal/lms"
	"ymca/internal/state"
)

type appTransport func(*http.Request) (*http.Response, error)

func (f appTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func appFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../lms/testdata/" + name + ".html")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func executeApp(args ...string) (string, string, error) {
	var out, stderr bytes.Buffer
	cmd := New(&out, &stderr)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), stderr.String(), err
}

// A transport fixture avoids real LMS requests and exercises the production
// origin, authentication checks, form parsing, CLI state and download paths.
func appLMS(t *testing.T) (string, *[]string) {
	t.Helper()
	dir := t.TempDir()
	err := (state.Store{Dir: dir}).WithLock(context.Background(), func(s *state.State) error { s.Session = &state.Session{SID: "fixture"}; return nil })
	if err != nil {
		t.Fatal(err)
	}
	dashboard, course := appFixture(t, "dashboard"), appFixture(t, "course")
	material := appFixture(t, "material")
	material = strings.Replace(material, `</table>`, `<tr><td><a onclick="downloadFile('FIL-second','SRS-second');">second.pdf</a></td></tr></table>`, 1)
	report := appFixture(t, "report")
	calls := []string{}
	original := http.DefaultTransport
	http.DefaultTransport = appTransport(func(r *http.Request) (*http.Response, error) {
		path := strings.Split(r.URL.Path, ";")[0]
		calls = append(calls, path)
		body := ""
		h := http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}
		r.ParseForm()
		switch path {
		case "/lms/homeHoml/doIndex":
			body = dashboard
		case "/lms/homeHoml/linkKougi":
			id := r.Form.Get("kougiId")
			code := "801105Z"
			switch id {
			case "KOG-a":
			case "KOG-b":
				code = "123456A"
			case "KOG-c":
				code = "123456B"
			case "KOG-d":
				code = "123456C"
			default:
				t.Fatalf("unexpected course %s", id)
			}
			body = strings.ReplaceAll(course, "801105Z", code)
		case "/lms/corsColl/invokeLoad":
			body = course
		case "/lms/corsColl/linkKyozaiTitle":
			switch r.Form.Get("kyozaiId") {
			case "SRY-example":
				body = material
			case "RPT-example":
				body = report
			default:
				t.Fatal("unexpected item", r.Form)
			}
		case "/lms/infrInfl/":
			body = appFixture(t, "notices")
		case "/lms/infrInal/index":
			body = `<table><tr><td>担当教員から</td><td><a onclick="clickTitle('INF-example')">Example notice</a></td><td>Example Office</td><td>2026/10/15</td><td>2026/10/31</td></tr></table>`
		case "/lms/infrInfr/":
			if r.Form.Get("infId") != "INF-example" {
				t.Fatal("notice selection lost", r.Form.Get("infId"))
			}
			body = appFixture(t, "notice")
		case "/lms/srcsSrcl/downloadFile":
			id := r.Form.Get("fileId")
			calls = append(calls, "file:"+id)
			if id != "FIL-example" && id != "FIL-second" {
				t.Fatal("unexpected file", id)
			}
			body = "%PDF-1.7 fixture"
			h.Set("Content-Type", "application/pdf")
			h.Set("Content-Disposition", `attachment; filename="example.pdf"`)
		default:
			t.Fatalf("unexpected request %s", path)
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	return dir, &calls
}

func TestScopedCommandsAndPersistentCodeMapping(t *testing.T) {
	dir, calls := appLMS(t)
	for _, selector := range []string{"801105Z", "KOG-a"} {
		out, _, err := executeApp("--state-dir", dir, "courses", selector, "materials", "list", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var got []lms.Material
		if err = json.Unmarshal([]byte(out), &got); err != nil || len(got) != 1 || got[0].CourseID != "KOG-a" || got[0].ID != "SRY-example" {
			t.Fatalf("%s %v", out, err)
		}
	}
	// Persisted cache prevents scanning every course in a subsequent CLI invocation.
	*calls = nil
	out, _, err := executeApp("courses", "801105Z", "show", "--state-dir", dir, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var detail lms.CourseDetail
	if err = json.Unmarshal([]byte(out), &detail); err != nil || detail.ID != "KOG-a" {
		t.Fatalf("%s %v", out, err)
	}
	entries := 0
	for _, path := range *calls {
		if path == "/lms/homeHoml/linkKougi" {
			entries++
		}
	}
	if entries != 1 {
		t.Fatalf("cached lookup fetched %d entry pages: %v", entries, *calls)
	}
	out, _, err = executeApp("courses", "KOG-a", "show", "--state-dir", dir)
	if err != nil || strings.Contains(out, "RPT-example") || !strings.Contains(out, "Materials: 1") {
		t.Fatalf("human summary: %s %v", out, err)
	}
	out, _, err = executeApp("courses", "KOG-a", "reports", "RPT-example", "show", "--state-dir", dir, "--json")
	var assignment lms.Assignment
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(out), &assignment); err != nil || assignment.Deadline == nil || strings.Contains(assignment.Content, "PRIVATE DRAFT") {
		t.Fatalf("report: %s %v", out, err)
	}
	out, _, err = executeApp("courses", "801105Z", "syllabus", "--state-dir", dir, "--url-only")
	if err != nil || !strings.Contains(out, "lct_cd=801105Z") || !strings.Contains(out, "lct_year=2025") {
		t.Fatalf("syllabus: %s %v", out, err)
	}
}

func TestScopedDownloadSelectionAndCollisions(t *testing.T) {
	dir, calls := appLMS(t)
	output := t.TempDir()
	for i := 0; i < 2; i++ {
		out, _, err := executeApp("courses", "KOG-a", "materials", "SRY-example", "download", "--file", "FIL-second", "--output", output, "--state-dir", dir, "--json")
		if err != nil {
			t.Fatal(err)
		}
		var result []map[string]string
		if err = json.Unmarshal([]byte(out), &result); err != nil || len(result) != 1 || result[0]["file_id"] != "FIL-second" {
			t.Fatalf("%s %v", out, err)
		}
		if b, err := os.ReadFile(result[0]["path"]); err != nil || !bytes.HasPrefix(b, []byte("%PDF")) {
			t.Fatal("download missing", err)
		}
	}
	entries, _ := os.ReadDir(output)
	if len(entries) != 2 {
		t.Fatal("collision overwrote a file")
	}
	for _, path := range *calls {
		if path == "file:FIL-example" {
			t.Fatal("unselected attachment downloaded")
		}
	}
	out, _, err := executeApp("courses", "KOG-a", "materials", "SRY-example", "download", "--file", "FIL-missing", "--output", output, "--state-dir", dir)
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "attachment was not found") || out != "" {
		t.Fatal("unknown file", out, err)
	}
}

func TestBulkDownloadKeepsSuccessfulFiles(t *testing.T) {
	dir, _ := appLMS(t)
	// Fail the second file while retaining the first one.
	original := http.DefaultTransport
	http.DefaultTransport = appTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "downloadFile") {
			r.ParseForm()
			if r.Form.Get("fileId") == "FIL-second" {
				return nil, errors.New("fixture download failure")
			}
		}
		return original.RoundTrip(r)
	})
	output := t.TempDir()
	out, stderr, err := executeApp("--state-dir", dir, "courses", "KOG-a", "materials", "download", "--all", "--output", output, "--json")
	if err != nil || !strings.Contains(stderr, "Warning:") || !strings.Contains(stderr, "FIL-second") {
		t.Fatal(err, stderr)
	}
	var results []map[string]string
	if e := json.Unmarshal([]byte(out), &results); e != nil || len(results) != 2 {
		t.Fatal(out, e)
	}
	if !strings.Contains(results[0]["path"], filepath.Join("日本語", "第１回")) {
		t.Fatal(results)
	}
	if _, e := os.Stat(results[0]["path"]); e != nil || results[1]["warning"] == "" || results[1]["error"] != "" {
		t.Fatal("partial failure lost successful file", results, e)
	}
}

func TestScopedGrammarValidationBeforeAuthentication(t *testing.T) {
	for _, args := range [][]string{
		{"courses", "my-physics", "materials", "list"},
		{"courses", "801105Z", "material", "show", "SRY-example"},
		{"courses", "801105Z", "items"},
		{"courses", "801105Z", "materials", "SRY-example", "typo"},
		{"courses", "801105Z", "materials", "show"},
		{"courses", "801105Z", "materials", "download"},
		{"courses", "801105Z", "materials", "download", "--all", "--file", "FIL-example"},
		{"courses", "801105Z", "materials", "list", "--course", "KOG-a"},
		{"courses", "801105Z", "reports", "list", "extra"},
		{"courses", "801105Z", "syllabus", "--year", "bad"},
		{"notices", "list", "--course", "KOG-a"},
		{"courses", "list", "--course", "KOG-a"},
		{"courses", "show", "KOG-a"},
		{"materials", "list", "--course", "KOG-a"},
		{"assignments", "list"},
		{"dashboard"},
		{"courses", "801105Z", "materials", "download", "--lesson", "第１回"},
		{"courses", "801105Z", "materials", "SRY-example", "download", "--lesson", "第１回", "--all"},
		{"courses", "801105Z", "materials", "download", "--all", "--lesson", ""},
		{"courses", "801105Z", "materials", "list", "--lesson", " "},
	} {
		dir := filepath.Join(t.TempDir(), "untouched")
		_, _, err := executeApp(append([]string{"--state-dir", dir}, args...)...)
		if ExitCode(err) != 2 {
			t.Fatalf("%v: %v (exit %d)", args, err, ExitCode(err))
		}
		if _, e := os.Stat(dir); !os.IsNotExist(e) {
			t.Fatalf("invalid usage touched session state: %v", args)
		}
	}
}

func TestScopedHelpAndCompletionAreOffline(t *testing.T) {
	for _, args := range [][]string{
		{"courses", "--help"},
		{"courses", "801105Z", "materials", "download", "--help"},
		{"help", "courses", "801105Z", "reports"},
		{"__complete", "courses", "801105Z", ""},
		{"__complete", "courses", "801105Z", "materials", ""},
		{"__complete", "courses", "801105Z", "materials", "download", "--"},
	} {
		dir := filepath.Join(t.TempDir(), "untouched")
		out, _, err := executeApp(append([]string{"--state-dir", dir}, args...)...)
		if err != nil || out == "" {
			t.Fatalf("%v: %s %v", args, out, err)
		}
		if _, e := os.Stat(dir); !os.IsNotExist(e) {
			t.Fatalf("help/completion touched state: %v", args)
		}
		if args[len(args)-1] == "--" && (strings.Count(out, "--output\t") != 1 || strings.Count(out, "--json\t") != 1) {
			t.Fatal("incorrect flag suggestions", out)
		}
	}
}
