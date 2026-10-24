package lms

import (
	"errors"
	"fmt"
	stdhtml "html"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

type MaterialDetail struct {
	Material Material   `json:"material"`
	Content  string     `json:"content"`
	Files    []Material `json:"files"`
}

func MaterialFolder(m Material) string {
	if m.Section == "" {
		return "materials"
	}
	return safeFilename(m.Section)
}

func CourseFolder(course Course) string {
	if strings.TrimSpace(course.Name) == "" {
		return "course"
	}
	return safeFilename(course.Name)
}

func downloadNavigation(p Page, a *html.Node) (navigation, error) {
	fn := "formDownload"
	args := arguments(attr(a, "onclick"), fn)
	if len(args) == 0 {
		fn = "downloadFile"
		args = arguments(attr(a, "onclick"), fn)
	}
	if (fn == "formDownload" && len(args) != 1) || (fn == "downloadFile" && len(args) != 2) {
		return navigationFor(p, a)
	}
	script, _ := functionBody(p.Body, fn)
	action := regexp.MustCompile(`\$\(["']#([^"']+)["']\)\.attr\(\s*['"]action['"]\s*,\s*['"]([^'"]+)['"]`).FindStringSubmatch(script)
	if len(action) != 3 {
		return navigation{}, ErrStructure
	}
	u := mustURL(absoluteURL(p.URL, action[2]))
	expected := "/lms/download/file"
	if fn == "downloadFile" {
		expected = "/lms/srcsSrcl/downloadFile"
	}
	if strings.Split(u.Path, ";")[0] != expected {
		return navigation{}, ErrStructure
	}
	f := findForm(collectForms(p.Body), "id", action[1])
	if f == nil {
		return navigation{}, ErrStructure
	}
	v := formValues(f)
	v.Set("fileId", args[0])
	if fn == "downloadFile" {
		v.Set("syosaiId", args[1])
	}
	return navigation{method: "POST", url: u.String(), values: v}, nil
}
func (c *Client) materialDetail(m Material) (*MaterialDetail, error) {
	p, err := c.page(m.request.method, m.request.url, m.request.values)
	if err != nil {
		return nil, err
	}
	p, err = c.loadBlocks(p)
	if err != nil {
		return nil, err
	}
	n := contentNode(document(p.Body))
	if n == nil {
		return nil, ErrStructure
	}
	files, err := materialLinks(p, m.CourseID)
	if err != nil {
		// A materials page may contain only text. It is recognized by its material
		// ID in a rendered form; unknown layouts still fail rather than look empty.
		recognized := byID(document(p.Body), "srcl_form") != nil || first(document(p.Body), func(n *html.Node) bool {
			return n.Data == "input" && attr(n, "name") == "kyozaiId" && attr(n, "value") == m.ID
		}) != nil
		if !recognized {
			return nil, err
		}
		files = []Material{}
	}
	for i := range files {
		files[i].Section = m.Section
	}
	if table := byClass(document(p.Body), "courseTable3"); table != nil {
		n = table
	}
	return &MaterialDetail{Material: m, Content: paragraphText(n), Files: files}, nil
}
func (c *Client) Material(id, course string) (*MaterialDetail, error) {
	materials, err := c.Materials(course)
	if err != nil {
		return nil, err
	}
	for _, m := range materials {
		if m.ID == id {
			return c.materialDetail(m)
		}
	}
	return nil, fmt.Errorf("material was not found: %w", ErrNotFound)
}
func (c *Client) ReportPage(id, course string) (Page, Assignment, error) {
	p, summary, err := c.coursePage(course)
	if err != nil {
		return Page{}, Assignment{}, err
	}
	d, err := ParseCoursePage(p.Body, summary)
	if err != nil {
		return Page{}, Assignment{}, err
	}
	for _, lesson := range d.Lessons {
		for _, item := range lesson.Items {
			if item.ID != id {
				continue
			}
			if item.Kind != "レポート" && !strings.EqualFold(item.Kind, "Report") {
				return Page{}, Assignment{}, errors.New("this item is not a report assignment")
			}
			a := first(document(p.Body), func(n *html.Node) bool {
				args := arguments(attr(n, "onclick"), "kyozaiTitleLink")
				return len(args) == 2 && args[0] == id
			})
			detail, err := c.follow(p, a)
			if err != nil {
				return Page{}, Assignment{}, err
			}
			detail, err = c.loadBlocks(detail)
			return detail, Assignment{ID: id, CourseID: course, Title: item.Title, Section: lesson.Name, SubmissionStatus: item.Status, AvailableUntil: item.AvailableUntil}, err
		}
	}
	return Page{}, Assignment{}, fmt.Errorf("report assignment %s was not found: %w", id, ErrNotFound)
}

func ParseReportPage(body string, summary Assignment) (*Assignment, error) {
	doc := document(body)
	cells := labeledCells(doc)
	deadline := cellValue(cells, "提出期間", "提出期限", "提出締切", "提出締切日時", "提出終了日時", "Submission deadline", "Due date")
	content := cellValue(cells, "課題内容", "内容", "レポート内容", "Content")
	if subject := byID(doc, "HTMLEditor_subject"); subject != nil {
		content = subject
	}
	if deadline == nil && content == nil {
		return nil, fmt.Errorf("%w: report detail fields are missing", ErrStructure)
	}
	if dates := datePattern.FindAllString(text(deadline), -1); len(dates) > 0 {
		summary.Deadline = parseDate(dates[len(dates)-1])
	}
	summary.SubmissionPeriod = text(deadline)
	if content != nil {
		summary.Content = paragraphText(content)
	}
	if summary.Content == "" && byID(doc, "HTMLEditor_subject") != nil {
		literal := regexp.MustCompile(`var\s+subjectCon\s*=\s*('(?:\\[\s\S]|[^'\\])*'|"(?:\\[\s\S]|[^"\\])*")`).FindStringSubmatch(body)
		if len(literal) == 2 {
			decoded, err := decodeJSLiteral(literal[1])
			if err != nil {
				return nil, ErrStructure
			}
			if unescaped, err := url.PathUnescape(decoded); err == nil {
				decoded = unescaped
			}
			summary.Content = paragraphText(document(stdhtml.UnescapeString(decoded)))
		}
	}
	if status := cellValue(cells, "状況", "提出状況", "提出状態", "Submission status"); status != nil {
		summary.SubmissionStatus = text(status)
	}
	return &summary, nil
}

// ReadMaterial follows the verified navigation retained by a Materials result.
// Batch callers can fetch each detail without re-reading the course list.
func (c *Client) ReadMaterial(m Material) (*MaterialDetail, error) {
	return c.materialDetail(m)
}
