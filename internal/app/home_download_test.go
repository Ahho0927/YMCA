package app

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ymca/internal/lms"
)

func TestProfileAndTopicsUseOneHomeRequest(t *testing.T) {
	dir, calls := appLMS(t)
	for _, resource := range []string{"profile", "topics"} {
		*calls = nil
		action := "show"
		if resource == "topics" {
			action = "list"
		}
		out, stderr, err := executeApp("--state-dir", dir, resource, action, "--json")
		if err != nil || stderr != "" {
			t.Fatal(err, stderr)
		}
		if !reflect.DeepEqual(*calls, []string{"/lms/homeHoml/doIndex"}) {
			t.Fatal("fetched unrelated details", *calls)
		}
		if resource == "profile" {
			var p profileResult
			if err = json.Unmarshal([]byte(out), &p); err != nil || p.Name != "Example Student" || !p.Verified || p.PreviousLogin == nil {
				t.Fatal(out, err)
			}
			if strings.Contains(out, "courses") || strings.Contains(out, "notices") {
				t.Fatal("profile includes unrelated summaries", out)
			}
		} else {
			var topics []lms.Topic
			if err = json.Unmarshal([]byte(out), &topics); err != nil || len(topics) != 1 || topics[0].ID != "0" || topics[0].Title != "System information" || topics[0].Date == nil {
				t.Fatal(out, err)
			}
		}
		if strings.Contains(out, "SID=") {
			t.Fatal("authentication secret leaked")
		}
	}
}

func TestRootHelpAndRemovedDashboardAreOffline(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "untouched")
	out, _, err := executeApp("--state-dir", dir)
	if err != nil || !strings.Contains(out, "profile") || !strings.Contains(out, "topics") || strings.Contains(out, "dashboard") {
		t.Fatal(out, err)
	}
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("root help accessed state")
	}
	for _, args := range [][]string{{"dashboard"}, {"dashboard", "--file", "unused.html"}} {
		_, _, err = executeApp(args...)
		if ExitCode(err) != 2 || !strings.Contains(err.Error(), "profile show") {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"profile", "show", "--help"}, {"topics", "list", "--help"}, {"__complete", "topics", ""}, {"__complete", "profile", ""}} {
		out, _, err = executeApp(append([]string{"--state-dir", dir}, args...)...)
		if err != nil || out == "" {
			t.Fatal(args, out, err)
		}
	}
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("help/completion accessed state")
	}
}

func TestOfflineTopicsEmptyAndInvalid(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "home.html")
	body := `<form id=homeHomlForm></form><div class=homeProfName>Student</div><div class=weeklyCourseArea></div>`
	if err := os.WriteFile(input, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeApp("topics", "list", "--file", input, "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatal(out, err)
	}
	if err = os.WriteFile(input, []byte(`<form id="gakuninLoginForm"></form>`), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err = executeApp("profile", "show", "--file", input)
	if ExitCode(err) != 3 {
		t.Fatal("login HTML accepted as a profile", err)
	}
}

// Synthetic lessons contain two SRY groups in the first lesson and one in the
// second. Each material contains two files. Intercepts never contact the LMS.
func lessonDownloadLMS(t *testing.T, duplicateNames bool) (string, *[]string) {
	t.Helper()
	dir, _ := appLMS(t)
	course := appFixture(t, "course")
	start := strings.Index(course, `<tr><td class="hide"></td><td id="SRY-example">`)
	end := start + strings.Index(course[start:], "</tr>") + len("</tr>")
	row := course[start:end]
	firstExtra := strings.ReplaceAll(row, "SRY-example", "SRY-first-extra")
	second := strings.ReplaceAll(row, "SRY-example", "SRY-second")
	origin := http.DefaultTransport
	seen := []string{}
	http.DefaultTransport = appTransport(func(r *http.Request) (*http.Response, error) {
		path := strings.Split(r.URL.Path, ";")[0]
		r.ParseForm()
		if path == "/lms/corsColl/linkKyozaiTitle" {
			id := r.Form.Get("kyozaiId")
			seen = append(seen, "material:"+id)
			body := appFixture(t, "material")
			body = strings.ReplaceAll(body, "FIL-example", "FIL-"+id+"-one")
			body = strings.Replace(body, `</table>`, `<tr><td><a onclick="downloadFile('FIL-`+id+`-two','SRS-second');">second.pdf</a></td></tr></table>`, 1)
			return fixtureResponse(r, body, "text/html"), nil
		}
		if path == "/lms/srcsSrcl/downloadFile" {
			id := r.Form.Get("fileId")
			seen = append(seen, "file:"+id)
			resp := fixtureResponse(r, "%PDF fixture", "application/pdf")
			resp.Header.Set("Content-Disposition", `attachment; filename="`+id+`.pdf"`)
			return resp, nil
		}
		resp, err := origin.RoundTrip(r)
		if err != nil {
			return nil, err
		}
		if path == "/lms/homeHoml/linkKougi" || path == "/lms/corsColl/invokeLoad" {
			b, e := io.ReadAll(resp.Body)
			resp.Body.Close()
			if e != nil {
				return nil, e
			}
			body := strings.Replace(string(b), row, row+firstExtra, 1)
			body = strings.Replace(body, `<tr><td class="hide"></td><td id="RPT-example">`, second+`<tr><td class="hide"></td><td id="RPT-example">`, 1)
			if duplicateNames {
				body = strings.ReplaceAll(body, "第２回（オンデマンド）", "第１回")
			}
			return fixtureResponse(r, body, "text/html"), nil
		}
		return resp, nil
	})
	return dir, &seen
}

func fixtureResponse(r *http.Request, body, media string) *http.Response {
	return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func TestDownloadAllAtEachScope(t *testing.T) {
	cases := []struct {
		name          string
		args          []string
		groups, files int
	}{
		{"course", []string{"download", "--all"}, 3, 6},
		{"lesson", []string{"download", "--lesson", "第１回", "--all"}, 2, 4},
		{"material", []string{"SRY-example", "download", "--all"}, 1, 2},
		{"material-default", []string{"SRY-example", "download"}, 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, seen := lessonDownloadLMS(t, false)
			output := t.TempDir()
			args := []string{"--state-dir", dir, "courses", "KOG-a", "materials"}
			args = append(args, tc.args...)
			args = append(args, "--output", output, "--json")
			out, _, err := executeApp(args...)
			if err != nil {
				t.Fatal(err)
			}
			var results []map[string]string
			if err = json.Unmarshal([]byte(out), &results); err != nil || len(results) != tc.files {
				t.Fatal(out, err)
			}
			groups, files := 0, 0
			for _, call := range *seen {
				if strings.HasPrefix(call, "material:") {
					groups++
					if tc.name == "lesson" && call == "material:SRY-second" {
						t.Fatal("downloaded another lesson")
					}
				}
				if strings.HasPrefix(call, "file:") {
					files++
				}
			}
			if groups != tc.groups || files != tc.files {
				t.Fatal(*seen)
			}
			for _, item := range results {
				if _, err = os.Stat(item["path"]); err != nil {
					t.Fatal(err)
				}
				if tc.name == "course" || tc.name == "lesson" {
					if !strings.Contains(item["path"], filepath.Join("日本語", "第")) {
						t.Fatal("bulk folder missing", item)
					}
				} else if filepath.Dir(item["path"]) != output {
					t.Fatal("SRY scope changed destination", item)
				}
			}
		})
	}
}

func TestLessonSelectionSafety(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "ambiguous"}[duplicate], func(t *testing.T) {
			dir, seen := lessonDownloadLMS(t, duplicate)
			name := "missing"
			want := "not found"
			if duplicate {
				name = "第１回"
				want = "ambiguous"
			}
			out, _, err := executeApp("--state-dir", dir, "courses", "KOG-a", "materials", "download", "--all", "--lesson", name, "--output", t.TempDir(), "--json")
			if ExitCode(err) != 1 || !strings.Contains(err.Error(), want) || out != "" || len(*seen) != 0 {
				t.Fatal(out, err, *seen)
			}
		})
	}
	dir, _ := appLMS(t)
	out, _, err := executeApp("--state-dir", dir, "courses", "KOG-a", "materials", "download", "--all", "--lesson", "第２回（オンデマンド）", "--output", t.TempDir(), "--json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Fatal("valid empty lesson must succeed", out, err)
	}
}

func TestMaterialsListLessonFilter(t *testing.T) {
	dir, seen := lessonDownloadLMS(t, false)
	out, _, err := executeApp("--state-dir", dir, "courses", "KOG-a", "materials", "list", "--lesson", "第１回", "--json")
	var materials []lms.Material
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(out), &materials); err != nil || len(materials) != 2 {
		t.Fatal(out, err)
	}
	if len(*seen) != 0 {
		t.Fatal("list fetched material details", *seen)
	}
	for _, m := range materials {
		if m.Section != "第１回" {
			t.Fatal(m)
		}
	}
}
