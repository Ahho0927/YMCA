package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ymca/internal/lms"
)

func TestIDBeforeActionReadsResources(t *testing.T) {
	dir, _ := appLMS(t)
	out, _, err := executeApp("courses", "KOG-a", "materials", "--json", "SRY-example", "--state-dir", dir, "show")
	if err != nil {
		t.Fatal(err)
	}
	var material lms.MaterialDetail
	if err = json.Unmarshal([]byte(out), &material); err != nil || material.Material.ID != "SRY-example" || len(material.Files) != 2 {
		t.Fatal(out, err)
	}
	out, _, err = executeApp("--state-dir", dir, "courses", "801105Z", "reports", "RPT-example", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report lms.Assignment
	if err = json.Unmarshal([]byte(out), &report); err != nil || report.ID != "RPT-example" || report.Deadline == nil {
		t.Fatal(out, err)
	}
	out, _, err = executeApp("--json", "notices", "--state-dir", dir, "INF-example", "show")
	if err != nil {
		t.Fatal(err)
	}
	var notice lms.NoticeDetail
	if err = json.Unmarshal([]byte(out), &notice); err != nil || notice.ID != "INF-example" || len(notice.Attachments) != 1 {
		t.Fatal(out, err)
	}
	out, _, err = executeApp("--state-dir", dir, "notices", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var notices []lms.Notice
	if err = json.Unmarshal([]byte(out), &notices); err != nil || len(notices) != 1 || notices[0].ID != "INF-example" {
		t.Fatal(out, err)
	}
}

func TestOldItemOrderFailsBeforeSessionAccess(t *testing.T) {
	for _, args := range [][]string{
		{"courses", "801105Z", "materials", "show", "SRY-example"},
		{"courses", "801105Z", "materials", "download", "SRY-example", "--all"},
		{"courses", "801105Z", "reports", "show", "RPT-example"},
		{"notices", "show", "INF-example"},
	} {
		dir := filepath.Join(t.TempDir(), "untouched")
		_, _, err := executeApp(append([]string{"--state-dir", dir}, args...)...)
		if ExitCode(err) != 2 || !strings.Contains(err.Error(), "before the action") {
			t.Fatalf("%v: %v", args, err)
		}
		if _, e := os.Stat(dir); !os.IsNotExist(e) {
			t.Fatal("old syntax touched state", args)
		}
	}
}

func TestItemIDHelpAndCompletionAreOffline(t *testing.T) {
	cases := []struct {
		args         []string
		want, absent string
	}{
		{[]string{"courses", "801105Z", "materials", "SRY-example", "show", "--help"}, "materials SRY-example show", ""},
		{[]string{"help", "courses", "801105Z", "materials", "SRY-example", "download"}, "materials SRY-example download", "--lesson"},
		{[]string{"courses", "801105Z", "materials", "download", "--help"}, "--lesson", "--file"},
		{[]string{"help", "notices", "INF-example", "show"}, "notices INF-example show", ""},
		{[]string{"__complete", "courses", "801105Z", "materials", "SRY-example", ""}, "show\t", "<material-id>"},
		{[]string{"__complete", "courses", "801105Z", "materials", ""}, "list\t", "show\t"},
		{[]string{"__complete", "courses", "801105Z", "materials", "SRY-example", "download", "--"}, "--file\t", "--lesson\t"},
		{[]string{"__complete", "courses", "801105Z", "reports", "RPT-example", ""}, "show\t", "<report-id>"},
		{[]string{"__complete", "notices", "INF-example", ""}, "show\t", "<notice-id>"},
		{[]string{"__complete", "notices", ""}, "list\t", "<notice-id>"},
		{[]string{"__complete", "notices", "INF-example", "show", "--"}, "--json\t", ""},
	}
	for _, tc := range cases {
		dir := filepath.Join(t.TempDir(), "untouched")
		out, _, err := executeApp(append([]string{"--state-dir", dir}, tc.args...)...)
		if err != nil || !strings.Contains(out, tc.want) || (tc.absent != "" && strings.Contains(out, tc.absent)) {
			t.Fatalf("%v: %s %v", tc.args, out, err)
		}
		if _, e := os.Stat(dir); !os.IsNotExist(e) {
			t.Fatal("help/completion touched state", tc.args)
		}
	}
}

func TestScopedFlagValuesDoNotBecomeIdentifiers(t *testing.T) {
	dir, _ := appLMS(t)
	output := filepath.Join(t.TempDir(), "SRY-output-with-show-and-reports")
	out, _, err := executeApp("--state-dir", dir, "courses", "KOG-a", "materials", "SRY-example", "download", "--file", "FIL-second", "--output", output, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var results []map[string]string
	if err = json.Unmarshal([]byte(out), &results); err != nil || len(results) != 1 || results[0]["file_id"] != "FIL-second" || filepath.Dir(results[0]["path"]) != output {
		t.Fatal(out, err)
	}
}
