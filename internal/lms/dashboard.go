package lms

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var quoted = regexp.MustCompile(`['"]([^'"]*)['"]`)
var datePattern = regexp.MustCompile(`\d{4}/\d{1,2}/\d{1,2}(?:\s*(?:\([^)]*\)|（[^）]*）))?(?:\s+\d{1,2}:\d{2})?`)
var jst = time.FixedZone("JST", 9*60*60)

type Account struct {
	Name          string     `json:"name"`
	Role          string     `json:"role"`
	PreviousLogin *time.Time `json:"previous_login"`
}
type Notice struct {
	ID      string     `json:"id"`
	Type    string     `json:"type"`
	Title   string     `json:"title"`
	Date    *time.Time `json:"date"`
	Body    string     `json:"body,omitempty"`
	Sender  string     `json:"sender,omitempty"`
	Expires *time.Time `json:"expires,omitempty"`
	Unread  *bool      `json:"unread,omitempty"`
}
type Topic struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Date  *time.Time `json:"date"`
}
type Course struct {
	ID          string `json:"id"`
	Code        string `json:"code,omitempty"`
	Year        string `json:"year,omitempty"`
	Name        string `json:"name"`
	Day         string `json:"day"`
	Period      string `json:"period"`
	Term        string `json:"term"`
	Teacher     string `json:"teacher"`
	Provisional bool   `json:"provisional"`
}
type Dashboard struct {
	Account       Account  `json:"account"`
	Year          string   `json:"year"`
	SelectedTerms []string `json:"selected_terms"`
	Notices       []Notice `json:"notices"`
	Topics        []Topic  `json:"topics"`
	Courses       []Course `json:"courses"`
	Verified      bool     `json:"authenticated_request"`
	Keepalive     any      `json:"keepalive,omitempty"`
}

func parseDate(s string) *time.Time {
	s = datePattern.FindString(s)
	s = regexp.MustCompile(`\s*(?:\([^)]*\)|（[^）]*）)`).ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ")
	nextDay := strings.HasSuffix(s, " 24:00")
	if nextDay {
		s = strings.TrimSuffix(s, "24:00") + "00:00"
	}
	for _, layout := range []string{"2006/1/2 15:04", "2006/1/2"} {
		if t, e := time.ParseInLocation(layout, s, jst); e == nil {
			if nextDay {
				t = t.AddDate(0, 0, 1)
			}
			return &t
		}
	}
	return nil
}
func arguments(s, fn string) []string {
	if !strings.Contains(s, fn+"(") {
		return nil
	}
	start := strings.Index(s, fn+"(") + len(fn) + 1
	end := strings.Index(s[start:], ")")
	if end < 0 {
		return nil
	}
	out := []string{}
	for _, m := range quoted.FindAllStringSubmatch(s[start:start+end], -1) {
		out = append(out, m[1])
	}
	return out
}
func dayRank(s string) int {
	for i, d := range []string{"月曜日", "火曜日", "水曜日", "木曜日", "金曜日", "土曜日", "日曜日", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"} {
		if s == d {
			return i % 7
		}
	}
	return 7
}
func ParseDashboard(body string) (*Dashboard, error) {
	d := document(body)
	if byID(d, "gakuninLoginForm") != nil {
		return nil, ErrAuth
	}
	if byID(d, "homeHomlForm") == nil || byClass(d, "weeklyCourseArea") == nil || byClass(d, "homeProfName") == nil {
		return nil, ErrStructure
	}
	out := &Dashboard{Notices: []Notice{}, Topics: []Topic{}, Courses: []Course{}, SelectedTerms: []string{}}
	out.Account = Account{Name: text(byClass(d, "homeProfName")), Role: text(byClass(d, "homeProfHeader")), PreviousLogin: parseDate(text(byClass(d, "last_login_date")))}
	year := first(d, func(n *html.Node) bool { return n.Data == "select" && attr(n, "name") == "nendo" })
	if year != nil {
		sel := selectData{}
		for _, n := range nodes(year, func(n *html.Node) bool { return n.Data == "option" }) {
			sel.options = append(sel.options, attributes(n))
		}
		out.Year = selectedOptionValue(sel)
	}
	// This CoursePower page initializes selected terms in inline JavaScript.
	re := regexp.MustCompile(`var newdata\s*=\s*['"]([^'"]+)['"]`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if !contains(out.SelectedTerms, m[1]) {
			out.SelectedTerms = append(out.SelectedTerms, m[1])
		}
	}
	for _, n := range nodes(d, func(n *html.Node) bool {
		return n.Data == "input" && attr(n, "name") == "checkKiList" && hasAttr(n, "checked")
	}) {
		v := attr(n, "value")
		if !contains(out.SelectedTerms, v) {
			out.SelectedTerms = append(out.SelectedTerms, v)
		}
	}
	for _, n := range nodes(d, func(n *html.Node) bool { return n.Data == "a" }) {
		onclick := attr(n, "onclick")
		if args := arguments(onclick, "openInfrInfr"); len(args) == 2 {
			out.Notices = append(out.Notices, Notice{ID: args[0], Type: args[1], Title: text(n), Date: parseDate(text(n.Parent))})
		}
		if args := arguments(onclick, "openTpicTpic"); len(args) == 1 {
			out.Topics = append(out.Topics, Topic{ID: args[0], Title: text(n), Date: parseDate(text(n.Parent))})
		}
	}
	day := "その他"
	seen := map[string]bool{}
	for _, n := range nodes(byClass(d, "weeklyCourseArea"), func(*html.Node) bool { return true }) {
		if class(n, "cpLabel") {
			s := text(n)
			if dayRank(s) < 7 || s == "その他" || s == "Other" {
				day = s
			}
		}
		if !class(n, "courseCard") {
			continue
		}
		a := first(n, func(x *html.Node) bool { return len(arguments(attr(x, "onclick"), "formSubmit")) > 0 })
		if a == nil {
			return nil, fmt.Errorf("%w: course identifier is missing", ErrStructure)
		}
		id := arguments(attr(a, "onclick"), "formSubmit")[0]
		info := byClass(n, "courseCardInfo")
		role := ""
		for _, x := range nodes(n, func(x *html.Node) bool { return class(x, "courseCardRole") }) {
			role += " " + text(x)
		}
		course := Course{ID: id, Year: out.Year, Name: text(byClass(n, "courseCardName")), Day: day, Period: directText(info), Term: text(byClass(info, "term")), Teacher: text(byClass(n, "courseCardUser")), Provisional: strings.Contains(role, "仮登録") || strings.Contains(strings.ToLower(role), "provisional")}
		key := id + "\n" + day + "\n" + course.Period
		if !seen[key] {
			out.Courses = append(out.Courses, course)
			seen[key] = true
		}
	}
	sort.SliceStable(out.Courses, func(i, j int) bool { return dayRank(out.Courses[i].Day) < dayRank(out.Courses[j].Day) })
	return out, nil
}
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
