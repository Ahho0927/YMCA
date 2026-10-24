package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"ymca/internal/lms"
)

func TestCourseFolderNamesAreDistinctAndStable(t *testing.T) {
	courses := []lms.Course{
		{ID: "KOG-b", Name: "日本語"},
		{ID: "KOG-a", Name: "日本語"},
		{ID: "KOG-c", Name: "../日本語"},
		{ID: "KOG-d", Name: "日本語 (1)"},
		{ID: "KOG-e", Name: "CON"},
		{ID: "KOG-f", Name: "con"},
		{ID: "KOG-g", Name: strings.Repeat("長い講義名", 80)},
		{ID: "KOG-h", Name: strings.Repeat("長い講義名", 81)},
		{ID: "KOG-i", Name: ""},
		{ID: "KOG-a", Name: "日本語"}, // The same ID can appear on multiple days.
	}
	names := courseFolderNames(courses)
	if names["KOG-a"] != "日本語" || names["KOG-b"] != "日本語 (1)" || names["KOG-c"] != "日本語 (2)" {
		t.Fatal(names)
	}
	seen := map[string]bool{}
	for id, name := range names {
		key := strings.ToLower(name)
		if seen[key] || !utf8.ValidString(name) || len(name) > 240 || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			t.Fatal("unsafe or colliding name", id, name)
		}
		seen[key] = true
	}
	for i, j := 0, len(courses)-1; i < j; i, j = i+1, j-1 {
		courses[i], courses[j] = courses[j], courses[i]
	}
	if !reflect.DeepEqual(names, courseFolderNames(courses)) {
		t.Fatal("folder names depend on dashboard ordering")
	}
}

func TestDuplicateCourseFolderMatchesSingleCourseDownload(t *testing.T) {
	dir, _ := appLMS(t)
	output := t.TempDir()
	for _, selector := range []string{"KOG-b", "123456A"} {
		out, stderr, err := executeApp("--state-dir", dir, "courses", selector, "materials", "*", "download", "--output", output, "--json")
		if err != nil || strings.Contains(stderr, "Warning:") {
			t.Fatal(err, stderr)
		}
		var rows []map[string]string
		if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 2 {
			t.Fatal(out, err)
		}
		for _, row := range rows {
			rel, err := filepath.Rel(output, row["path"])
			if err != nil || !strings.HasPrefix(rel, "日本語 (1)"+string(filepath.Separator)) {
				t.Fatal(row, err)
			}
		}
	}
}

func TestDownloadWarningsSkipMaterialsAndFiles(t *testing.T) {
	dir, _ := lessonDownloadLMS(t, false)
	original := http.DefaultTransport
	http.DefaultTransport = appTransport(func(r *http.Request) (*http.Response, error) {
		path := strings.Split(r.URL.Path, ";")[0]
		r.ParseForm()
		if path == "/lms/corsColl/linkKyozaiTitle" {
			switch r.Form.Get("kyozaiId") {
			case "SRY-example":
				return fixtureResponse(r, `<a class="logoutButton">Logout</a><form id="srcl_form"></form><div class="contentsColumn">Text only</div>`, "text/html"), nil
			case "SRY-first-extra":
				return nil, errors.New("fixture detail unavailable")
			}
		}
		if path == "/lms/srcsSrcl/downloadFile" && r.Form.Get("fileId") == "FIL-SRY-second-one" {
			resp := fixtureResponse(r, "unavailable", "text/plain")
			resp.StatusCode = http.StatusNotFound
			return resp, nil
		}
		return original.RoundTrip(r)
	})
	out, stderr, err := executeApp("--state-dir", dir, "courses", "KOG-a", "materials", "*", "download", "--output", t.TempDir(), "--json")
	if err != nil || strings.Count(stderr, "Warning:") != 3 || !strings.Contains(stderr, "no downloadable attachments") || !strings.Contains(stderr, "HTTP 404") {
		t.Fatal(err, stderr)
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 4 {
		t.Fatal(out, err)
	}
	warnings, saved := 0, 0
	for _, row := range rows {
		if row["error"] != "" {
			t.Fatal("skip remained an error", row)
		}
		if row["warning"] != "" {
			warnings++
			if !strings.Contains(stderr, row["id"]) {
				t.Fatal("warning omitted material ID", stderr)
			}
			continue
		}
		saved++
		if row["file_id"] != "FIL-SRY-second-two" {
			t.Fatal("wrong file downloaded", row)
		}
		if _, err := os.Stat(row["path"]); err != nil {
			t.Fatal(err)
		}
	}
	if warnings != 3 || saved != 1 {
		t.Fatal(rows)
	}
}

func TestSingleUnavailableFileIsWarning(t *testing.T) {
	dir, _ := appLMS(t)
	original := http.DefaultTransport
	http.DefaultTransport = appTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "downloadFile") {
			return nil, errors.New("fixture disconnected")
		}
		return original.RoundTrip(r)
	})
	out, stderr, err := executeApp("--state-dir", dir, "courses", "KOG-a", "materials", "SRY-example", "download", "FIL-second", "--output", t.TempDir(), "--json")
	if err != nil || !strings.Contains(stderr, "Warning:") || !strings.Contains(stderr, "FIL-second") {
		t.Fatal(err, stderr)
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 || rows[0]["warning"] == "" || rows[0]["path"] != "" {
		t.Fatal(out, err)
	}
}

func TestDownloadFileExpiryStillStops(t *testing.T) {
	dir, _ := appLMS(t)
	original := http.DefaultTransport
	requests := 0
	http.DefaultTransport = appTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "downloadFile") {
			requests++
			return fixtureResponse(r, `<form id="gakuninLoginForm"></form>`, "text/html"), nil
		}
		return original.RoundTrip(r)
	})
	out, stderr, err := executeApp("--state-dir", dir, "courses", "*", "materials", "*", "download", "--output", t.TempDir(), "--json")
	if ExitCode(err) != 3 || requests != 1 || strings.Contains(stderr, "Warning:") {
		t.Fatal(err, stderr, requests)
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 || rows[0]["error"] == "" || rows[0]["warning"] != "" {
		t.Fatal(out, err)
	}
}
