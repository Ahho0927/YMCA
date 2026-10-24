package lms

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type Detail struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
}
type Assignment struct {
	ID               string     `json:"id"`
	CourseID         string     `json:"course_id"`
	Title            string     `json:"title"`
	Deadline         *time.Time `json:"deadline"`
	SubmissionStatus string     `json:"submission_status"`
	Content          string     `json:"content,omitempty"`
	Section          string     `json:"section,omitempty"`
	AvailableUntil   *time.Time `json:"available_until,omitempty"`
	SubmissionPeriod string     `json:"submission_period,omitempty"`
}
type Material struct {
	ID             string     `json:"id"`
	CourseID       string     `json:"course_id"`
	Title          string     `json:"title"`
	Section        string     `json:"section"`
	Status         string     `json:"status,omitempty"`
	AvailableUntil *time.Time `json:"available_until,omitempty"`
	request        navigation
	landing        bool
}
type navigation struct {
	method, url string
	values      url.Values
	fragment    bool
}

func (c *Client) dashboardPage() (Page, error) {
	if c.sid == "" {
		return Page{}, ErrAuth
	}
	return c.page("GET", c.lmsOrigin+"/lms/homeHoml/doIndex;SID="+url.PathEscape(c.sid), nil)
}
func functionBody(body, name string) (string, []string) {
	re := regexp.MustCompile(`function\s+` + regexp.QuoteMeta(name) + `\s*\(([^)]*)\)\s*\{`)
	m := re.FindStringSubmatchIndex(body)
	if m == nil {
		return "", nil
	}
	params := strings.Split(body[m[2]:m[3]], ",")
	for i := range params {
		params[i] = strings.TrimSpace(params[i])
	}
	start := m[1]
	depth := 1
	var quote byte
	escaped := false
	for i := start; i < len(body); i++ {
		b := body[i]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == quote {
				quote = 0
			}
			continue
		}
		if b == '\'' || b == '"' || b == '`' {
			quote = b
			continue
		}
		if b == '{' {
			depth++
		}
		if b == '}' {
			depth--
			if depth == 0 {
				return body[start:i], params
			}
		}
	}
	return "", nil
}
func (c *Client) homeSubmit(p Page, fn string, overrides url.Values) (Page, error) {
	script, _ := functionBody(p.Body, fn)
	re := regexp.MustCompile(`\.attr\(\s*['"]action['"]\s*,\s*['"]([^'"]+)['"]\s*\)`)
	m := re.FindStringSubmatch(script)
	if len(m) < 2 {
		return Page{}, fmt.Errorf("%w: %s navigation", ErrStructure, fn)
	}
	f := findForm(collectForms(p.Body), "id", "homeHomlForm")
	if f == nil {
		return Page{}, ErrStructure
	}
	values := formValues(f)
	if d, err := ParseDashboard(p.Body); err == nil {
		values["checkKiList"] = d.SelectedTerms
	}
	for k, v := range overrides {
		values[k] = v
	}
	return c.page("POST", absoluteURL(p.URL, m[1]), values)
}
func (c *Client) courseEntry(id string) (Page, Course, error) {
	p, err := c.dashboardPage()
	if err != nil {
		return Page{}, Course{}, err
	}
	d, err := ParseDashboard(p.Body)
	if err != nil {
		return Page{}, Course{}, err
	}
	var course *Course
	for _, v := range d.Courses {
		if v.ID == id {
			v := v
			course = &v
			break
		}
	}
	if course == nil {
		return Page{}, Course{}, errors.New("course was not found in your current LMS course list")
	}
	p, err = c.homeSubmit(p, "formSubmit", url.Values{"kougiId": {id}})
	if err == nil && byClass(document(p.Body), "courseName") != nil {
		c.rememberCourseCode(course.Year, id, courseCode(document(p.Body)))
	}
	return p, *course, err
}
func (c *Client) coursePage(id string) (Page, Course, error) {
	p, summary, err := c.courseEntry(id)
	if err != nil {
		return Page{}, Course{}, err
	}
	p, err = c.loadBlocks(p)
	return p, summary, err
}
func contentNode(d *html.Node) *html.Node {
	for _, className := range []string{"contentsColumn", "contentsArea", "mainContents", "content"} {
		if n := byClass(d, className); n != nil {
			return n
		}
	}
	return nil
}
func (c *Client) Course(id string) (*CourseDetail, error) {
	p, course, err := c.coursePage(id)
	if err != nil {
		return nil, err
	}
	return ParseCoursePage(p.Body, course)
}

func parseNotices(body string) ([]Notice, error) {
	d := document(body)
	out := []Notice{}
	seen := map[string]bool{}
	for _, n := range nodes(d, func(n *html.Node) bool { return n.Data == "a" }) {
		args := arguments(attr(n, "onclick"), "openInfrInfr")
		listArgs := arguments(attr(n, "onclick"), "clickTitle")
		if len(args) != 2 && len(listArgs) != 1 {
			continue
		}
		container := n.Parent
		for container.Parent != nil && container.Data != "tr" && container.Data != "ul" && container.Data != "li" {
			container = container.Parent
		}
		item := Notice{Title: text(n), Date: parseDate(text(container))}
		if len(args) == 2 {
			item.ID = args[0]
			item.Type = args[1]
		} else {
			item.ID = listArgs[0]
			cells := directCells(container)
			if len(cells) < 5 {
				return nil, ErrStructure
			}
			switch category := text(cells[0]); {
			case strings.Contains(category, "担当教員"), strings.Contains(strings.ToLower(category), "instructor"):
				item.Type = "02"
			case strings.Contains(category, "自動"), strings.Contains(strings.ToLower(category), "automatic"):
				item.Type = "03"
			case strings.Contains(category, "管理者"), strings.Contains(strings.ToLower(category), "administrator"):
				item.Type = "01"
			}
			item.Sender = text(cells[2])
			item.Date = parseDate(text(cells[3]))
			item.Expires = parseDate(text(cells[4]))
			unread := first(n, func(x *html.Node) bool {
				return x.Data == "img" && (attr(x, "alt") == "未読" || strings.EqualFold(attr(x, "alt"), "unread"))
			}) != nil
			item.Unread = &unread
		}
		if !seen[item.ID] {
			out = append(out, item)
			seen[item.ID] = true
		}
	}
	if len(out) == 0 {
		// An explicitly rendered empty-state message is different from an unknown layout.
		t := text(d)
		if !strings.Contains(t, "お知らせはありません") && !strings.Contains(t, "該当するデータがありません") && !strings.Contains(t, "該当データがありません") && !strings.Contains(strings.ToLower(t), "no notices") {
			return nil, ErrStructure
		}
	}
	return out, nil
}
func nextNavigation(p Page) (*navigation, error) {
	for _, a := range nodes(document(p.Body), func(n *html.Node) bool { return n.Data == "a" }) {
		label := strings.ToLower(text(a))
		if label == "" {
			if img := first(a, func(n *html.Node) bool { return n.Data == "img" }); img != nil {
				label = strings.ToLower(attr(img, "alt"))
			}
		}
		if !class(a, "nextlink") && attr(a, "rel") != "next" && label != "next" && label != "次へ" && label != "次ページ" {
			continue
		}
		if class(a, "disabled") || attr(a, "aria-disabled") == "true" {
			continue
		}
		href := attr(a, "href")
		if href == "" || href == "#" || strings.HasPrefix(href, "javascript:") {
			pager := regexp.MustCompile(`initPager\(\s*(\d+)\s*,\s*['"]([^'"]+)['"]`).FindStringSubmatch(p.Body)
			if len(pager) == 3 {
				f := findForm(collectForms(p.Body), "id", "list_form")
				if f == nil {
					return nil, ErrStructure
				}
				values := formValues(f)
				if sort := regexp.MustCompile(`initSort\(\s*(-?\d+)`).FindStringSubmatch(p.Body); len(sort) == 2 {
					values.Set("sortKey", sort[1])
				}
				page, _ := strconv.Atoi(pager[1])
				values.Set("page", strconv.Itoa(page+1))
				return &navigation{method: "POST", url: absoluteURL(p.URL, pager[2]), values: values, fragment: true}, nil
			}
			return nil, fmt.Errorf("%w: JavaScript pagination requires an actual page capture", ErrStructure)
		}
		return &navigation{method: "GET", url: absoluteURL(p.URL, href)}, nil
	}
	return nil, nil
}
func (c *Client) Notices(course string) ([]Notice, error) {
	if course != "" {
		return nil, errors.New("the LMS global notice view does not expose a course-ID filter; use 'ymca notices list'")
	}
	p, err := c.dashboardPage()
	if err != nil {
		return nil, err
	}
	p, err = c.homeSubmit(p, "openInfrInfl", url.Values{"infType": {"00"}})
	if err != nil {
		return nil, err
	}
	p, err = c.loadBlocks(p)
	if err != nil {
		return nil, err
	}
	out := []Notice{}
	seen := map[string]bool{}
	pages := map[string]bool{}
	for count := 0; count < 100; count++ {
		key := p.URL
		if current := regexp.MustCompile(`initPager\(\s*(\d+)`).FindStringSubmatch(p.Body); len(current) == 2 {
			key += "\n" + current[1]
		}
		if pages[key] {
			return nil, errors.New("LMS pagination repeated a page")
		}
		pages[key] = true
		list, err := parseNotices(p.Body)
		if err != nil {
			return nil, err
		}
		for _, v := range list {
			if !seen[v.ID] {
				seen[v.ID] = true
				out = append(out, v)
			}
		}
		next, err := nextNavigation(p)
		if err != nil {
			return nil, err
		}
		if next == nil {
			return out, nil
		}
		previous := p
		p, err = c.readHTML(next.method, next.url, next.values, next.fragment)
		if err != nil {
			return nil, err
		}
		if next.fragment {
			// Pager fragments reuse the outer list form and detail handlers.
			f := findForm(collectForms(previous.Body), "id", "list_form")
			if f == nil {
				return nil, ErrStructure
			}
			p.Body += `<form id="list_form" method="post" action="` + html.EscapeString(f.attrs["action"]) + `">`
			for name, values := range next.values {
				for _, v := range values {
					p.Body += `<input type="hidden" name="` + html.EscapeString(name) + `" value="` + html.EscapeString(v) + `">`
				}
			}
			p.Body += "</form>"
		}
	}
	return nil, errors.New("LMS notice pagination exceeded 100 pages")
}
func (c *Client) Notice(id, course string) (*NoticeDetail, error) {
	if course != "" {
		return nil, errors.New("notice IDs are global; omit --course when reading a notice")
	}
	p, err := c.dashboardPage()
	if err != nil {
		return nil, err
	}
	d, err := ParseDashboard(p.Body)
	if err != nil {
		return nil, err
	}
	candidates := d.Notices
	var found *Notice
	for _, n := range candidates {
		if n.ID == id {
			n := n
			found = &n
		}
	}
	if found == nil {
		candidates, err = c.Notices(course)
		if err != nil {
			return nil, err
		}
		for _, n := range candidates {
			if n.ID == id {
				n := n
				found = &n
			}
		}
	}
	if found == nil {
		return nil, fmt.Errorf("notice was not found: %w", ErrNotFound)
	}
	p, err = c.homeSubmit(p, "openInfrInfr", url.Values{"infId": {id}, "infType": {found.Type}})
	if err != nil {
		return nil, err
	}
	return ParseNoticePage(p, id)
}

// Resource navigation is accepted only when the page contains the link/form.
// Unsupported JavaScript is rejected rather than guessing endpoints or executing submissions.
func navigationFor(p Page, a *html.Node) (navigation, error) {
	href := attr(a, "href")
	if href != "" && href != "#" && !strings.HasPrefix(strings.ToLower(href), "javascript:") {
		u := absoluteURL(p.URL, href)
		if unsafeAction(u) {
			return navigation{}, ErrStructure
		}
		return navigation{method: "GET", url: u}, nil
	}
	onclick := attr(a, "onclick")
	call := regexp.MustCompile(`^\s*(?:return\s+)?([A-Za-z_$][\w$]*)\s*\(`).FindStringSubmatch(onclick)
	if len(call) < 2 {
		return navigation{}, ErrStructure
	}
	body, params := functionBody(p.Body, call[1])
	if body == "" {
		return navigation{}, ErrStructure
	}
	// Reject branching and network code: static extraction cannot safely interpret it.
	if regexp.MustCompile(`\b(if|switch|fetch|ajax|eval)\s*\(`).MatchString(body) {
		return navigation{}, ErrStructure
	}
	action := regexp.MustCompile(`\$\(['"]#([^'"]+)['"]\)\.attr\(\s*['"]action['"]\s*,\s*['"]([^'"]+)['"]\s*\)`).FindStringSubmatch(body)
	if len(action) < 3 || unsafeAction(action[2]) {
		return navigation{}, ErrStructure
	}
	f := findForm(collectForms(p.Body), "id", action[1])
	if f == nil {
		return navigation{}, ErrStructure
	}
	values := formValues(f)
	args := arguments(onclick, call[1])
	mapping := map[string]string{}
	for i, param := range params {
		if i < len(args) {
			mapping[param] = args[i]
		}
	}
	assignments := regexp.MustCompile(`\$\(['"]#([^'"]+?)(?::hidden)?['"]\)\.(?:attr\(\s*['"]value['"]\s*,\s*|val\(\s*)([A-Za-z_$][\w$]*|'[^']*'|"[^"]*")\s*\)`).FindAllStringSubmatch(body, -1)
	for _, m := range assignments {
		input := byID(document(p.Body), m[1])
		name := attr(input, "name")
		if name == "" {
			return navigation{}, ErrStructure
		}
		v, ok := mapping[m[2]]
		if !ok {
			if len(m[2]) > 1 && (m[2][0] == '\'' || m[2][0] == '"') {
				v = m[2][1 : len(m[2])-1]
			} else {
				return navigation{}, ErrStructure
			}
		}
		values.Set(name, v)
	}
	return navigation{method: "POST", url: absoluteURL(p.URL, action[2]), values: values}, nil
}
func unsafeAction(raw string) bool {
	s := strings.ToLower(raw)
	for _, v := range []string{"logout", "submit", "register", "delete", "update", "insert", "answer", "attendance", "finish", "execute", "save", "send"} {
		if strings.Contains(s, v) {
			return true
		}
	}
	return false
}
func (c *Client) follow(p Page, a *html.Node) (Page, error) {
	nav, err := navigationFor(p, a)
	if err != nil {
		return Page{}, err
	}
	return c.page(nav.method, nav.url, nav.values)
}

// materialLinks recognizes actual attachment links, never an arbitrary list of every link.
func materialLinks(p Page, course string) ([]Material, error) {
	out := []Material{}
	for _, a := range nodes(document(p.Body), func(n *html.Node) bool { return n.Data == "a" }) {
		nav, err := downloadNavigation(p, a)
		if err != nil {
			continue
		}
		u := mustURL(nav.url)
		lower := strings.ToLower(u.Path)
		if !strings.Contains(lower, "download") && !hasAttr(a, "download") {
			continue
		}
		id := attr(a, "data-material-id")
		if id == "" {
			for _, key := range []string{"fileId", "kyozaiId", "materialId"} {
				id = u.Query().Get(key)
				if id == "" {
					id = nav.values.Get(key)
				}
				if id != "" {
					break
				}
			}
		}
		if id == "" {
			id = stableResourceID(nav)
		}
		title := text(a)
		if title == "" {
			title = attr(a, "download")
		}
		if title == "" {
			title = "Attachment"
		}
		section := ""
		for parent := a.Parent; parent != nil; parent = parent.Parent {
			if parent.Data == "tr" {
				section = text(parent)
				break
			}
		}
		out = append(out, Material{ID: id, CourseID: course, Title: title, Section: section, request: nav})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: attachment list not recognized (capture the course/material page)", ErrStructure)
	}
	return out, nil
}
func (c *Client) Materials(course string) ([]Material, error) {
	return c.MaterialsInLesson(course, "")
}

// MaterialsInLesson selects an exact displayed lesson name. It rejects missing
// or ambiguous names instead of using a transient screen ordinal as an ID.
func (c *Client) MaterialsInLesson(course, lessonName string) ([]Material, error) {
	p, summary, err := c.coursePage(course)
	if err != nil {
		return nil, err
	}
	d, err := ParseCoursePage(p.Body, summary)
	if err != nil {
		return nil, err
	}
	lessons, err := selectLessons(d.Lessons, lessonName)
	if err != nil {
		return nil, err
	}
	out := []Material{}
	doc := document(p.Body)
	for _, lesson := range lessons {
		for _, item := range lesson.Items {
			if item.TypeCode != "01" {
				continue
			}
			a := first(doc, func(n *html.Node) bool {
				args := arguments(attr(n, "onclick"), "kyozaiTitleLink")
				return len(args) == 2 && args[0] == item.ID
			})
			nav, e := navigationFor(p, a)
			if e != nil {
				return nil, fmt.Errorf("material %s: %w", item.ID, e)
			}
			out = append(out, Material{ID: item.ID, CourseID: course, Title: item.Title, Section: lesson.Name, Status: item.Status, AvailableUntil: item.AvailableUntil, request: nav, landing: true})
		}
	}
	return out, nil
}

func (c *Client) MaterialFiles(m Material) ([]Material, error) {
	if !m.landing {
		return []Material{m}, nil
	}
	d, err := c.materialDetail(m)
	if err != nil {
		return nil, err
	}
	if len(d.Files) == 0 {
		return nil, errors.New("this material contains no downloadable attachments")
	}
	return d.Files, nil
}
func (c *Client) Assignments(course string) ([]Assignment, error) {
	return c.assignments(course, false)
}

// AssignmentDetails includes instructions while reading each report only once.
func (c *Client) AssignmentDetails(course string) ([]Assignment, error) {
	return c.assignments(course, true)
}

func (c *Client) assignments(course string, includeContent bool) ([]Assignment, error) {
	if course == "" {
		d, err := c.Dashboard()
		if err != nil {
			return nil, err
		}
		out := []Assignment{}
		seen := map[string]bool{}
		for _, v := range d.Courses {
			if seen[v.ID] {
				continue
			}
			seen[v.ID] = true
			items, e := c.assignments(v.ID, includeContent)
			if e != nil {
				return nil, fmt.Errorf("course %s: %w", v.ID, e)
			}
			out = append(out, items...)
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Deadline == nil {
				return false
			}
			if out[j].Deadline == nil {
				return true
			}
			return out[i].Deadline.Before(*out[j].Deadline)
		})
		return out, nil
	}
	p, summary, err := c.coursePage(course)
	if err != nil {
		return nil, err
	}
	d, err := ParseCoursePage(p.Body, summary)
	if err != nil {
		return nil, err
	}
	out := []Assignment{}
	failures := []error{}
	seenReports := map[string]bool{}
	for _, lesson := range d.Lessons {
		for _, item := range lesson.Items {
			if item.Kind != "レポート" && !strings.EqualFold(item.Kind, "Report") {
				continue
			}
			if includeContent && seenReports[item.ID] {
				continue
			}
			seenReports[item.ID] = true
			// 公開期限 is an availability limit, not necessarily the report deadline.
			summary := Assignment{ID: item.ID, CourseID: course, Title: item.Title, SubmissionStatus: item.Status, Section: lesson.Name, AvailableUntil: item.AvailableUntil}
			a := first(document(p.Body), func(n *html.Node) bool {
				args := arguments(attr(n, "onclick"), "kyozaiTitleLink")
				return len(args) == 2 && args[0] == item.ID
			})
			report, err := c.readReportLink(p, a, summary)
			if err != nil {
				failure := fmt.Errorf("report %s: %w", item.ID, err)
				if !includeContent {
					return nil, failure
				}
				failures = append(failures, failure)
				if errors.Is(err, ErrAuth) || c.ctx.Err() != nil {
					return out, errors.Join(failures...)
				}
				continue
			}
			// Lists keep instruction bodies in the show command.
			if !includeContent {
				report.Content = ""
			}
			out = append(out, *report)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Deadline == nil {
			return false
		}
		if out[j].Deadline == nil {
			return true
		}
		return out[i].Deadline.Before(*out[j].Deadline)
	})
	return out, errors.Join(failures...)
}
func (c *Client) Assignment(id, course string) (*Assignment, error) {
	p, summary, err := c.ReportPage(id, course)
	if err != nil {
		return nil, err
	}
	return ParseReportPage(p.Body, summary)
}

// ReadNotice accepts a summary from Notices, avoiding reloading the entire
// paginated notice box for each individual detail in a wildcard selection.
func (c *Client) ReadNotice(n Notice) (*NoticeDetail, error) {
	p, err := c.dashboardPage()
	if err != nil {
		return nil, err
	}
	p, err = c.homeSubmit(p, "openInfrInfr", url.Values{"infId": {n.ID}, "infType": {n.Type}})
	if err != nil {
		return nil, err
	}
	return ParseNoticePage(p, n.ID)
}

func (c *Client) readReportLink(p Page, link *html.Node, summary Assignment) (*Assignment, error) {
	page, err := c.follow(p, link)
	if err != nil {
		return nil, err
	}
	page, err = c.loadBlocks(page)
	if err != nil {
		return nil, err
	}
	return ParseReportPage(page.Body, summary)
}
