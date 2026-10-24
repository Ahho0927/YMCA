package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ymca/internal/lms"
)

func TestWildcardDownloads(t *testing.T) {
	cases := []struct {
		name        string
		scope       []string
		count       int
		multiCourse bool
	}{
		{"all-materials", []string{"KOG-a", "materials", "*", "download"}, 6, false},
		{"all-files", []string{"KOG-a", "materials", "SRY-example", "download", "*"}, 2, false},
		{"file-id", []string{"KOG-a", "materials", "SRY-example", "download", "FIL-SRY-example-two"}, 1, false},
		{"lesson", []string{"KOG-a", "materials", "*", "download", "*", "--lesson", "第１回"}, 4, false},
		{"all-courses", []string{"*", "materials", "*", "download", "*"}, 24, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, _ := lessonDownloadLMS(t, false)
			output := t.TempDir()
			args := append([]string{"--state-dir", dir, "courses"}, tc.scope...)
			args = append(args, "--output", output, "--json")
			out, _, err := executeApp(args...)
			if err != nil {
				t.Fatal(err)
			}
			var rows []map[string]string
			if err = json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != tc.count {
				t.Fatal(out, err)
			}
			courses := map[string]bool{}
			folders := map[string]string{"KOG-a": "日本語", "KOG-b": "日本語 (1)", "KOG-c": "Calculus", "KOG-d": "【連絡専用】Student Support"}
			for _, row := range rows {
				if _, err = os.Stat(row["path"]); err != nil {
					t.Fatal(row, err)
				}
				if tc.multiCourse {
					courses[row["course_id"]] = true
					if !strings.Contains(row["path"], folders[row["course_id"]]+string(filepath.Separator)) {
						t.Fatal("course folder missing", row)
					}
				}
				if tc.name == "lesson" && row["id"] == "SRY-second" {
					t.Fatal("downloaded another lesson", row)
				}
				if tc.name == "file-id" && row["file_id"] != "FIL-SRY-example-two" {
					t.Fatal("downloaded another file", row)
				}
			}
			if tc.multiCourse && len(courses) != 4 {
				t.Fatal("missing courses", courses)
			}
		})
	}
}

func TestWildcardQueryShapesAndBodies(t *testing.T) {
	dir, seen := lessonDownloadLMS(t, false)
	out, _, err := executeApp("--state-dir", dir, "courses", "KOG-a", "materials", "*", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var materials []lms.MaterialDetail
	if err = json.Unmarshal([]byte(out), &materials); err != nil || len(materials) != 3 {
		t.Fatal(out, err)
	}
	if len(*seen) != 3 {
		t.Fatal("detail traversal fetched extra materials/files", *seen)
	}
	for _, m := range materials {
		if len(m.Files) != 2 || m.Material.CourseID != "KOG-a" {
			t.Fatal(m)
		}
	}
	out, _, err = executeApp("--state-dir", dir, "courses", "*", "materials", "*", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(out), &materials); err != nil || len(materials) != 12 {
		t.Fatal(out, err)
	}
}

func TestWildcardReportsCoursesAndSyllabus(t *testing.T) {
	dir, _ := appLMS(t)
	out, _, err := executeApp("--state-dir", dir, "courses", "*", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var courses []lms.CourseDetail
	if err = json.Unmarshal([]byte(out), &courses); err != nil || len(courses) != 4 {
		t.Fatal(out, err)
	}
	out, _, err = executeApp("--state-dir", dir, "courses", "*", "reports", "*", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var reports []lms.Assignment
	if err = json.Unmarshal([]byte(out), &reports); err != nil || len(reports) != 4 {
		t.Fatal(out, err)
	}
	for _, r := range reports {
		if !strings.Contains(r.Content, "Write your own answer") || strings.Contains(r.Content, "PRIVATE DRAFT") || r.Deadline == nil {
			t.Fatal(r)
		}
	}
	out, _, err = executeApp("--state-dir", dir, "courses", "*", "reports", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	reports = nil
	if err = json.Unmarshal([]byte(out), &reports); err != nil || len(reports) != 4 {
		t.Fatal(out, err)
	}
	for _, r := range reports {
		if r.Content != "" {
			t.Fatal("list included bodies", r)
		}
	}
	out, _, err = executeApp("--state-dir", dir, "courses", "*", "syllabus", "--url-only")
	if err != nil || len(strings.Split(strings.TrimSpace(out), "\n")) != 4 {
		t.Fatal(out, err)
	}
}

func TestWildcardNoticesDoNotReloadTheNoticeList(t *testing.T) {
	dir, _ := appLMS(t)
	original := http.DefaultTransport
	lists, details := 0, 0
	http.DefaultTransport = appTransport(func(r *http.Request) (*http.Response, error) {
		switch strings.Split(r.URL.Path, ";")[0] {
		case "/lms/infrInal/index":
			lists++
			body := `<table>`
			for _, id := range []string{"INF-example", "INF-second"} {
				body += `<tr><td>担当教員から</td><td><a onclick="clickTitle('` + id + `')">Example notice</a></td><td>Office</td><td>2026/10/15</td><td>2026/10/31</td></tr>`
			}
			return fixtureResponse(r, body+`</table>`, "text/html"), nil
		case "/lms/infrInfr/":
			r.ParseForm()
			details++
			return fixtureResponse(r, strings.ReplaceAll(appFixture(t, "notice"), "INF-example", r.Form.Get("infId")), "text/html"), nil
		}
		return original.RoundTrip(r)
	})
	out, _, err := executeApp("--state-dir", dir, "notices", "*", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var notices []lms.NoticeDetail
	if err = json.Unmarshal([]byte(out), &notices); err != nil || len(notices) != 2 || lists != 1 || details != 2 {
		t.Fatal(out, err, lists, details)
	}
	for _, n := range notices {
		if n.Content != "First line\nSecond line\nNext paragraph" {
			t.Fatal(n)
		}
	}
}

func TestWildcardBatchFailureAndAuthStop(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "network", true: "expired"}[expired], func(t *testing.T) {
			dir, _ := appLMS(t)
			original := http.DefaultTransport
			selected := []string{}
			http.DefaultTransport = appTransport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "linkKougi") {
					r.ParseForm()
					id := r.Form.Get("kougiId")
					selected = append(selected, id)
					if id == "KOG-b" {
						if expired {
							return fixtureResponse(r, `<form id="gakuninLoginForm"></form>`, "text/html"), nil
						}
						return nil, errors.New("fixture disconnected")
					}
				}
				return original.RoundTrip(r)
			})
			out, _, err := executeApp("--state-dir", dir, "courses", "*", "show", "--json")
			wantCode, wantItems := 1, 3
			if expired {
				wantCode, wantItems = 3, 1
			}
			if ExitCode(err) != wantCode {
				t.Fatal(err)
			}
			var courses []lms.CourseDetail
			if e := json.Unmarshal([]byte(out), &courses); e != nil || len(courses) != wantItems {
				t.Fatal(out, e)
			}
			for _, course := range courses {
				if course.ID == "" {
					t.Fatal("failed query inserted an empty item", out)
				}
			}
			if expired && strings.Join(selected, ",") != "KOG-a,KOG-b" {
				t.Fatal("requested data after expiry", selected)
			}
		})
	}
}

func TestWildcardDownloadPartialFailure(t *testing.T) {
	dir, _ := appLMS(t)
	original := http.DefaultTransport
	http.DefaultTransport = appTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "linkKougi") {
			r.ParseForm()
			if r.Form.Get("kougiId") == "KOG-b" {
				return nil, errors.New("fixture disconnected")
			}
		}
		return original.RoundTrip(r)
	})
	out, stderr, err := executeApp("--state-dir", dir, "courses", "*", "materials", "*", "download", "*", "--output", t.TempDir(), "--json")
	if err != nil || !strings.Contains(stderr, "Warning: course KOG-b") {
		t.Fatal(err, stderr)
	}
	var rows []map[string]string
	if e := json.Unmarshal([]byte(out), &rows); e != nil || len(rows) != 7 {
		t.Fatal(out, e)
	}
	failures := 0
	for _, r := range rows {
		if r["error"] != "" {
			t.Fatal("recoverable failure is still an error", r)
		}
		if r["warning"] != "" {
			failures++
			if r["course_id"] != "KOG-b" {
				t.Fatal(r)
			}
		} else if _, e := os.Stat(r["path"]); e != nil {
			t.Fatal("success lost", r, e)
		}
	}
	if failures != 1 {
		t.Fatal(rows)
	}
}

func TestWildcardValidationHelpAndCompletion(t *testing.T) {
	invalid := [][]string{
		{"courses", "KOG*", "show"},
		{"courses", "KOG-a", "materials", "SRY*", "show"},
		{"courses", "KOG-a", "materials", "SRY-example", "download", "FIL*"},
		{"courses", "KOG-a", "materials", "SRY-example", "download", "FIL-second", "--all"},
		{"courses", "KOG-a", "materials", "SRY-example", "download", "*", "--file", "FIL-second"},
		{"courses", "KOG-a", "materials", "SRY-example", "download", "FIL-one", "FIL-two"},
	}
	for _, args := range invalid {
		dir := filepath.Join(t.TempDir(), "untouched")
		_, _, err := executeApp(append([]string{"--state-dir", dir}, args...)...)
		if ExitCode(err) != 2 {
			t.Fatal(args, err)
		}
		if _, e := os.Stat(dir); !os.IsNotExist(e) {
			t.Fatal("invalid selector touched state", args)
		}
	}
	for _, args := range [][]string{
		{"help", "courses", "*", "materials", "*", "download"},
		{"help", "notices", "*", "show"},
		{"__complete", "courses", "*", "materials", "*", ""},
		{"__complete", "courses", "KOG-a", "materials", "SRY-example", "download", ""},
		{"__complete", "notices", ""},
	} {
		dir := filepath.Join(t.TempDir(), "untouched")
		out, _, err := executeApp(append([]string{"--state-dir", dir}, args...)...)
		if err != nil || out == "" {
			t.Fatal(args, out, err)
		}
		if _, e := os.Stat(dir); !os.IsNotExist(e) {
			t.Fatal("help/completion touched state", args)
		}
		if args[len(args)-1] == "" && args[len(args)-2] == "download" && !strings.Contains(out, "*\t") {
			t.Fatal("file wildcard not completed", out)
		}
	}
}

func TestWildcardEmptySelections(t *testing.T) {
	dir, _ := appLMS(t)
	original := http.DefaultTransport
	http.DefaultTransport = appTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "homeHoml/doIndex") {
			body := `<a class="logoutButton">Logout</a><form id="homeHomlForm"></form><div class="homeProfName">Student</div><div class="weeklyCourseArea"></div>`
			return fixtureResponse(r, body, "text/html"), nil
		}
		t.Fatal("empty wildcard selection performed extra requests", r.URL.Path)
		return original.RoundTrip(r)
	})
	for _, args := range [][]string{{"courses", "*", "show"}, {"courses", "*", "materials", "*", "download", "*"}} {
		out, _, err := executeApp(append([]string{"--state-dir", dir, "--json"}, args...)...)
		if err != nil || strings.TrimSpace(out) != "[]" {
			t.Fatal(args, out, err)
		}
	}
}

func TestWildcardDownloadStopsAfterExpiry(t *testing.T) {
	dir, _ := appLMS(t)
	original := http.DefaultTransport
	ids := []string{}
	http.DefaultTransport = appTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "linkKougi") {
			r.ParseForm()
			id := r.Form.Get("kougiId")
			ids = append(ids, id)
			if id == "KOG-b" {
				return fixtureResponse(r, `<form id="gakuninLoginForm"></form>`, "text/html"), nil
			}
		}
		return original.RoundTrip(r)
	})
	out, _, err := executeApp("--state-dir", dir, "courses", "*", "materials", "*", "download", "*", "--output", t.TempDir(), "--json")
	if ExitCode(err) != 3 || strings.Join(ids, ",") != "KOG-a,KOG-b" {
		t.Fatal(err, ids)
	}
	var rows []map[string]string
	if e := json.Unmarshal([]byte(out), &rows); e != nil || len(rows) != 3 || rows[2]["error"] == "" {
		t.Fatal(out, e)
	}
	for _, row := range rows[:2] {
		if _, e := os.Stat(row["path"]); e != nil {
			t.Fatal(e)
		}
	}
}

func TestFileIDUnderMaterialWildcard(t *testing.T) {
	dir, seen := lessonDownloadLMS(t, false)
	out, _, err := executeApp("--state-dir", dir, "courses", "KOG-a", "materials", "*", "download", "FIL-SRY-second-two", "--output", t.TempDir(), "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]string
	if e := json.Unmarshal([]byte(out), &rows); e != nil || len(rows) != 1 || rows[0]["id"] != "SRY-second" {
		t.Fatal(out, e)
	}
	files := 0
	for _, call := range *seen {
		if strings.HasPrefix(call, "file:") {
			files++
		}
	}
	if files != 1 {
		t.Fatal("downloaded unmatched files", *seen)
	}
}

func TestWildcardReportDetailFailureKeepsOtherReports(t *testing.T) {
	dir, _ := appLMS(t)
	fixture := appFixture(t, "course")
	start := strings.Index(fixture, `<tr><td class="hide"></td><td id="RPT-example">`)
	end := start + strings.Index(fixture[start:], "</tr>") + len("</tr>")
	row := fixture[start:end]
	original := http.DefaultTransport
	visited := []string{}
	http.DefaultTransport = appTransport(func(r *http.Request) (*http.Response, error) {
		path := strings.Split(r.URL.Path, ";")[0]
		if path == "/lms/corsColl/linkKyozaiTitle" {
			r.ParseForm()
			id := r.Form.Get("kyozaiId")
			visited = append(visited, id)
			if id == "RPT-bad" {
				return nil, errors.New("fixture report failure")
			}
			return fixtureResponse(r, strings.ReplaceAll(appFixture(t, "report"), "RPT-example", id), "text/html"), nil
		}
		resp, err := original.RoundTrip(r)
		if err != nil {
			return nil, err
		}
		if strings.Contains(path, "linkKougi") || path == "/lms/corsColl/invokeLoad" {
			b, e := io.ReadAll(resp.Body)
			resp.Body.Close()
			if e != nil {
				return nil, e
			}
			body := strings.Replace(string(b), row, row+row+strings.ReplaceAll(row, "RPT-example", "RPT-bad")+strings.ReplaceAll(row, "RPT-example", "RPT-after"), 1)
			return fixtureResponse(r, body, "text/html"), nil
		}
		return resp, nil
	})
	out, _, err := executeApp("--state-dir", dir, "courses", "KOG-a", "reports", "*", "show", "--json")
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "RPT-bad") {
		t.Fatal(err)
	}
	var reports []lms.Assignment
	if e := json.Unmarshal([]byte(out), &reports); e != nil || len(reports) != 2 || strings.Join(visited, ",") != "RPT-example,RPT-bad,RPT-after" {
		t.Fatal(out, e, visited)
	}
}
