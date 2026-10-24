package lms

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/html"
)

type CourseDetail struct {
	Detail
	Course  Course   `json:"course"`
	Lessons []Lesson `json:"lessons"`
}
type Lesson struct {
	Name        string       `json:"name"`
	Date        *time.Time   `json:"date"`
	Attendance  string       `json:"attendance"`
	Description string       `json:"description,omitempty"`
	Items       []CourseItem `json:"items"`
}
type CourseItem struct {
	ID             string     `json:"id"`
	TypeCode       string     `json:"type_code"`
	Kind           string     `json:"kind"`
	Title          string     `json:"title"`
	Availability   string     `json:"availability"`
	AvailableUntil *time.Time `json:"available_until"`
	Status         string     `json:"status"`
}

func directCells(row *html.Node) []*html.Node {
	out := []*html.Node{}
	if row == nil {
		return out
	}
	for n := row.FirstChild; n != nil; n = n.NextSibling {
		if n.Type == html.ElementNode && (n.Data == "td" || n.Data == "th") {
			out = append(out, n)
		}
	}
	return out
}
func directRows(table *html.Node) []*html.Node {
	out := []*html.Node{}
	if table == nil {
		return out
	}
	for n := table.FirstChild; n != nil; n = n.NextSibling {
		if n.Data == "tr" {
			out = append(out, n)
		}
		if n.Data == "tbody" || n.Data == "thead" || n.Data == "tfoot" {
			out = append(out, directRows(n)...)
		}
	}
	return out
}
func courseCode(doc *html.Node) string {
	// The final breadcrumb suffix is the lecture code, not the KOG internal ID.
	crumb := byClass(doc, "breadCrumbBar")
	if crumb == nil {
		crumb = byID(doc, "breadCrumbBar")
	}
	code := regexp.MustCompile(`\[([A-Za-z0-9]{7})\]\s*$`)
	for _, n := range nodes(crumb, func(n *html.Node) bool { return n.Data == "p" }) {
		if text(n) == text(byClass(doc, "courseName")) {
			continue
		}
		if m := code.FindStringSubmatch(text(n)); len(m) == 2 {
			return m[1]
		}
	}
	for _, n := range nodes(doc, func(n *html.Node) bool { return n.Data == "input" && attr(n, "name") == "kougiCode" }) {
		v := attr(n, "value")
		if regexp.MustCompile(`^[A-Za-z0-9]{7}$`).MatchString(v) {
			return v
		}
	}
	return ""
}
func ParseCoursePage(body string, course Course) (*CourseDetail, error) {
	doc := document(body)
	tableContainer := byClass(doc, "courseListTable")
	if byClass(doc, "courseName") == nil {
		return nil, fmt.Errorf("%w: course lesson table is missing", ErrStructure)
	}
	if name := text(byClass(doc, "courseName")); name != "" {
		course.Name = name
	}
	course.Code = courseCode(doc)
	out := &CourseDetail{Detail: Detail{ID: course.ID, Title: course.Name}, Course: course, Lessons: []Lesson{}}
	if tableContainer == nil {
		message := text(byClass(doc, "messageArea"))
		if strings.Contains(message, "授業が登録されていません") || strings.Contains(strings.ToLower(message), "no classes have been registered") {
			return out, nil
		}
		return nil, fmt.Errorf("%w: course lesson table is missing", ErrStructure)
	}
	table := first(tableContainer, func(n *html.Node) bool { return n.Data == "table" })
	var current *Lesson
	for _, row := range directRows(table) {
		if class(row, "jyugyoHidden") {
			cells := directCells(row)
			if len(cells) < 4 {
				return nil, ErrStructure
			}
			out.Lessons = append(out.Lessons, Lesson{Name: text(byClass(row, "courseFolderName")), Date: parseDate(text(cells[2])), Attendance: text(cells[3]), Items: []CourseItem{}})
			current = &out.Lessons[len(out.Lessons)-1]
			continue
		}
		if !class(row, "kyozaiHidden") {
			continue
		}
		if current == nil {
			return nil, ErrStructure
		}
		// Browsers foster-parent paragraph descriptions outside malformed nested tables.
		descriptions := []string{}
		for _, n := range nodes(row, func(n *html.Node) bool { return n.Data == "p" }) {
			if s := paragraphText(n); s != "" {
				descriptions = append(descriptions, s)
			}
		}
		current.Description = strings.Join(descriptions, "\n\n")
		for _, a := range nodes(row, func(n *html.Node) bool { return n.Data == "a" }) {
			args := arguments(attr(a, "onclick"), "kyozaiTitleLink")
			if len(args) != 2 {
				continue
			}
			tr := a.Parent
			for tr != nil && tr.Data != "tr" {
				tr = tr.Parent
			}
			cells := directCells(tr)
			if len(cells) < 4 {
				return nil, ErrStructure
			}
			img := first(cells[1], func(n *html.Node) bool { return n.Data == "img" })
			availability := text(cells[2])
			dates := datePattern.FindAllString(availability, -1)
			var until *time.Time
			if len(dates) > 0 {
				until = parseDate(dates[len(dates)-1])
			}
			current.Items = append(current.Items, CourseItem{ID: args[0], TypeCode: args[1], Kind: attr(img, "alt"), Title: text(a), Availability: availability, AvailableUntil: until, Status: text(cells[3])})
		}
	}
	out.Content = paragraphText(tableContainer)
	return out, nil
}

// paragraphText retains line breaks in lesson instructions and notice bodies.
func paragraphText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil || n.Data == "script" || n.Data == "style" {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(strings.Map(func(r rune) rune {
				if unicode.IsSpace(r) {
					return ' '
				}
				return r
			}, n.Data))
			return
		}
		block := n.Data == "p" || n.Data == "div" || n.Data == "tr" || n.Data == "li" || n.Data == "br" || n.Data == "h1" || n.Data == "h2" || n.Data == "h3"
		if block {
			b.WriteByte('\n')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
			if c.Data == "td" || c.Data == "th" {
				b.WriteString("  ")
			}
		}
		if block {
			b.WriteByte('\n')
		}
	}
	walk(n)
	lines := []string{}
	for _, line := range strings.Split(b.String(), "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func selectLessons(lessons []Lesson, name string) ([]Lesson, error) {
	if name == "" {
		return lessons, nil
	}
	selected := []Lesson{}
	for _, lesson := range lessons {
		if lesson.Name == name {
			selected = append(selected, lesson)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("lesson %q was not found; use 'courses <course> show' for exact lesson names", name)
	}
	if len(selected) > 1 {
		return nil, fmt.Errorf("lesson name %q is ambiguous (%d matches); no materials were selected", name, len(selected))
	}
	return selected, nil
}
