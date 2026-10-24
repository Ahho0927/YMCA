package lms

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/net/html"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func resourceFixture(t *testing.T, name string) string {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name + ".html")
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestCourseDetailStructure(t *testing.T) {
	d, e := ParseCoursePage(resourceFixture(t, "course"), Course{ID: "KOG-a", Year: "2026"})
	if e != nil {
		t.Fatal(e)
	}
	if d.Course.Code != "801105Z" || d.Course.Year != "2026" || len(d.Lessons) != 2 {
		t.Fatalf("metadata: %+v", d.Course)
	}
	if len(d.Lessons[0].Items) != 1 || len(d.Lessons[1].Items) != 2 {
		t.Fatal("nested resource rows lost")
	}
	if !strings.Contains(d.Lessons[1].Description, "example.\nKeep") {
		t.Fatal(d.Lessons[1].Description)
	}
	report := d.Lessons[1].Items[0]
	if report.Kind != "レポート" || report.AvailableUntil.Format("2006-01-02T15:04:05Z07:00") != "2026-10-22T00:00:00+09:00" {
		t.Fatal(report)
	}
	if _, e = ParseCoursePage("<html>changed layout</html>", Course{}); !errors.Is(e, ErrStructure) {
		t.Fatal(e)
	}
}

func TestCourseWithoutRegisteredLessons(t *testing.T) {
	input := `<div class="breadCrumbBar"><p>Example course[881207B]</p></div><div class="courseName">Example course</div><div class="messageArea addMessBox">授業が登録されていません。</div>`
	d, e := ParseCoursePage(input, Course{ID: "KOG-empty", Year: "2026"})
	if e != nil || d.Course.Code != "881207B" || d.Lessons == nil || len(d.Lessons) != 0 {
		t.Fatalf("empty course: %+v, %v", d, e)
	}
}

func TestLectureTitleIsNotMistakenForCode(t *testing.T) {
	input := `<div class="courseName">Example[Physics]</div><div class="breadCrumbBar"><p>Example[Physics]</p></div>`
	if code := courseCode(document(input)); code != "" {
		t.Fatalf("title was mistaken for lecture code: %s", code)
	}
}
func noticeFragment(id string, page int, next bool) string {
	link := ""
	if next {
		link = `<a class="nextlink" href="javascript:void(0)">次へ</a>`
	}
	return fmt.Sprintf(`<script>initPager(%d,'/lms/infrInal/;SID=fixture','/lms/infrInal/updateLimit;SID=fixture');initSort(-6,'/lms/infrInal/;SID=fixture');</script>%s<table class="cs_table2"><tr><th>分類</th><th>タイトル</th><th>発信者</th><th>公開開始</th><th>公開終了</th></tr><tr><td>担当教員から</td><td><a onclick="clickTitle('%s');">Example notice<img alt="未読"></a></td><td>Example Sender</td><td>2026/10/15 (木) 12:33</td><td>2026/10/31 (土) 24:00</td></tr></table>`, page, link, id)
}
func TestNoticesAJAXAndPagination(t *testing.T) {
	dashboard := fixture(t)
	shell := resourceFixture(t, "notices")
	fragments := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch strings.Split(r.URL.Path, ";")[0] {
		case "/lms/homeHoml/doIndex":
			fmt.Fprint(w, dashboard)
		case "/lms/infrInfl/":
			fmt.Fprint(w, shell)
		case "/lms/infrInal/index":
			fragments++
			if r.Method != "POST" {
				t.Fatal("AJAX must post")
			}
			fmt.Fprint(w, noticeFragment("INF-first", 1, true))
		case "/lms/infrInal/":
			fragments++
			r.ParseForm()
			if r.Form.Get("page") != "2" || r.Form.Get("limit") != "10" || r.Form.Get("sortKey") != "-6" {
				t.Error("pagination state lost", r.Form)
			}
			fmt.Fprint(w, noticeFragment("INF-second", 2, false))
		default:
			t.Error("unexpected path", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	list, e := c.Notices("")
	if e != nil {
		t.Fatal(e)
	}
	if len(list) != 2 || fragments != 2 {
		t.Fatalf("list=%d, fragments=%d", len(list), fragments)
	}
	if list[0].Type != "02" || list[0].Sender != "Example Sender" || list[0].Unread == nil || !*list[0].Unread || list[0].Date.Format("15:04") != "12:33" {
		t.Fatal(list[0])
	}
}
func TestNoticeDetailBodyAndAttachments(t *testing.T) {
	p := Page{URL: "https://lms.ynu.ac.jp/lms/infrInfr/", Body: resourceFixture(t, "notice")}
	d, e := ParseNoticePage(p, "INF-example")
	if e != nil {
		t.Fatal(e)
	}
	if d.Content != "First line\nSecond line\nNext paragraph" || strings.Contains(d.Content, "Sender") {
		t.Fatal(d.Content)
	}
	if d.Published.Format("15:04") != "12:33" || d.Expires.Format("2006-01-02 15:04") != "2026-11-01 00:00" {
		t.Fatal("notice time parsing")
	}
	if len(d.Attachments) != 1 || d.Attachments[0].request.values.Get("fileId") != "FIL-example" {
		t.Fatal(d.Attachments)
	}
	if _, e = ParseNoticePage(p, "INF-wrong"); !errors.Is(e, ErrStructure) {
		t.Fatal("wrong detail ID accepted")
	}
}
func TestCourseNavigationMaterialsAndReports(t *testing.T) {
	dashboard := fixture(t)
	course := resourceFixture(t, "course")
	material := resourceFixture(t, "material")
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch strings.Split(r.URL.Path, ";")[0] {
		case "/lms/homeHoml/doIndex":
			fmt.Fprint(w, dashboard)
		case "/lms/homeHoml/linkKougi":
			fmt.Fprint(w, course)
		case "/lms/corsColl/invokeLoad":
			fmt.Fprint(w, `<div class="courseListTable">`+innerCourseTable(course)+`</div>`)
		case "/lms/corsColl/linkKyozaiTitle":
			r.ParseForm()
			if r.Form.Get("kyozaiId") == "RPT-example" {
				fmt.Fprint(w, resourceFixture(t, "report"))
				return
			}
			if r.Form.Get("kyozaiId") != "SRY-example" || r.Form.Get("kyozaiSyCdHidden") != "01" {
				t.Error("resource selection lost")
			}
			fmt.Fprint(w, material)
		default:
			t.Error("unexpected path", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	d, e := c.Course("KOG-a")
	if e != nil || d.Course.Code != "801105Z" {
		t.Fatal(e)
	}
	materials, e := c.Materials("KOG-a")
	if e != nil {
		t.Fatal(e)
	}
	if len(materials) != 1 || materials[0].ID != "SRY-example" {
		t.Fatal(materials)
	}
	files, e := c.MaterialFiles(materials[0])
	if e != nil {
		t.Fatal(e)
	}
	if len(files) != 1 || files[0].request.values.Get("fileId") != "FIL-example" || files[0].request.values.Get("syosaiId") != "SRS-example" {
		t.Fatal(files)
	}
	reports, e := c.Assignments("KOG-a")
	if e != nil {
		t.Fatal(e)
	}
	if len(reports) != 1 || reports[0].Deadline == nil || reports[0].AvailableUntil == nil || reports[0].Deadline.Equal(*reports[0].AvailableUntil) {
		t.Fatal("report deadline was not read independently from availability", reports)
	}
}

func TestReportInstructionsAndDeadline(t *testing.T) {
	d, err := ParseReportPage(resourceFixture(t, "report"), Assignment{ID: "REP-example", Title: "Example report"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Deadline.Format("2006-01-02 15:04") != "2026-10-16 00:00" || d.SubmissionStatus != "未提出" {
		t.Fatal(d)
	}
	if d.Content != "Read the example.\nWrite your own answer." || strings.Contains(d.Content, "PRIVATE DRAFT") {
		t.Fatal(d.Content)
	}
}

func TestReportPeriodRangeAndNoDeadline(t *testing.T) {
	input := resourceFixture(t, "report")
	input = strings.Replace(input, "2026/10/15 (木) 24:00 まで", "2026/10/13 (火) 14:40 から2026/10/19 (月) 24:00 まで", 1)
	d, err := ParseReportPage(input, Assignment{})
	if err != nil || d.Deadline.Format("2006-01-02 15:04") != "2026-10-20 00:00" {
		t.Fatalf("period range: %+v, %v", d, err)
	}
	input = strings.Replace(resourceFixture(t, "report"), "2026/10/15 (木) 24:00 まで", "-", 1)
	d, err = ParseReportPage(input, Assignment{})
	if err != nil || d.Deadline != nil || d.SubmissionPeriod != "-" {
		t.Fatalf("unspecified deadline: %+v, %v", d, err)
	}
}

func TestJSLiteralDecoding(t *testing.T) {
	for _, v := range []struct{ source, want string }{{`'It\'s a \\ test'`, "It's a \\ test"}, {`'\u65e5\u672c\ud83d\ude00'`, "日本😀"}, {`"a\x20b\nnext"`, "a b\nnext"}} {
		got, err := decodeJSLiteral(v.source)
		if err != nil || got != v.want {
			t.Fatalf("decode: %q, %v", got, err)
		}
	}
}
func innerCourseTable(body string) string {
	n := byClass(document(body), "courseListTable")
	var b strings.Builder
	if n != nil {
		html.Render(&b, n)
	}
	return b.String()
}
func TestSyllabusCodeAndCookieFreeFetch(t *testing.T) {
	url, e := SyllabusURL("2026", "801105Z")
	if e != nil {
		t.Fatal(e)
	}
	q := mustURL(url).Query()
	if q.Get("lct_cd") != "801105Z" || q.Get("lct_year") != "2026" || q.Get("je_cd") != "1" {
		t.Fatal(url)
	}
	for _, bad := range []string{"KOG0000000163774", "801105Z&x=1", "123"} {
		if _, e = SyllabusURL("2026", bad); e == nil {
			t.Fatal("invalid code accepted")
		}
	}
	s := &Syllabus{Code: "801105Z", Year: "2026", URL: url}
	body := resourceFixture(t, "syllabus")
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if len(r.Cookies()) > 0 || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != "" {
			t.Fatal("LMS credentials leaked")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	if e = fetchSyllabus(context.Background(), s, rt); e != nil {
		t.Fatal(e)
	}
	if s.Title != "Example course" || len(s.Fields) != 2 || len(s.Sections) != 2 || !strings.Contains(s.Sections[0].Content, "aim.\nSecond") {
		t.Fatal(s)
	}
	s.Code = "0000000"
	if e = parseSyllabus(body, s); e == nil {
		t.Fatal("wrong syllabus accepted")
	}
}

func TestSyllabusRejectsCredentialTargetAndExternalRedirect(t *testing.T) {
	calls := 0
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://example.com/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	u, _ := SyllabusURL("2026", "801105Z")
	s := &Syllabus{Code: "801105Z", URL: u}
	if err := fetchSyllabus(context.Background(), s, rt); !errors.Is(err, errUntrustedTarget) || calls != 1 {
		t.Fatalf("external redirect: %v calls=%d", err, calls)
	}
	s.URL = "https://student:secret@risyu.jmk.ynu.ac.jp/"
	if err := fetchSyllabus(context.Background(), s, rt); !errors.Is(err, errUntrustedTarget) || calls != 1 {
		t.Fatal("credential-bearing URL accepted")
	}
}
func TestDateWithWeekdayAndMidnight(t *testing.T) {
	for _, v := range []struct{ input, want string }{{"2026/10/15 (木) 12:33", "2026-10-15T12:33:00+09:00"}, {"2026/12/31 24:00", "2027-01-01T00:00:00+09:00"}, {"2026/1/2", "2026-01-02T00:00:00+09:00"}} {
		d := parseDate(v.input)
		if d == nil || d.Format("2006-01-02T15:04:05Z07:00") != v.want {
			t.Fatalf("parse %s: %v", v.input, d)
		}
	}
	if parseDate("2026/10/15 24:30") != nil {
		t.Fatal("invalid time accepted")
	}
}
