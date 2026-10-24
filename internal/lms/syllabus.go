package lms

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const syllabusOrigin = "https://risyu.jmk.ynu.ac.jp"

type Syllabus struct {
	CourseID string          `json:"course_id"`
	Code     string          `json:"code"`
	Year     string          `json:"year"`
	URL      string          `json:"url"`
	Title    string          `json:"title,omitempty"`
	Fields   []SyllabusField `json:"fields,omitempty"`
	Sections []SyllabusField `json:"sections,omitempty"`
}
type SyllabusField struct {
	Label   string `json:"label"`
	Content string `json:"content"`
}

func SyllabusURL(year, code string) (string, error) {
	if !regexp.MustCompile(`^[0-9]{4}$`).MatchString(year) {
		return "", errors.New("syllabus year must have four digits; use --year if LMS provides no academic year")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9]{7}$`).MatchString(code) {
		return "", errors.New("this course has no seven-character lecture code for a public syllabus")
	}
	q := url.Values{"lct_year": {year}, "lct_cd": {code}, "je_cd": {"1"}}
	return syllabusOrigin + "/gakumu_portal/Public/Syllabus/DetailMain.aspx?" + q.Encode(), nil
}
func (c *Client) Syllabus(id, year string) (*Syllabus, error) {
	p, summary, err := c.courseEntry(id)
	if err != nil {
		return nil, err
	}
	if year == "" {
		year = summary.Year
	}
	code := courseCode(document(p.Body))
	u, err := SyllabusURL(year, code)
	if err != nil {
		return nil, err
	}
	title := text(byClass(document(p.Body), "courseName"))
	if title == "" {
		title = summary.Name
	}
	return &Syllabus{CourseID: id, Code: code, Year: year, URL: u, Title: title}, nil
}

// FetchSyllabus uses a separate cookie-free client. The academic portal never
// receives any LMS/SSO cookies, SIDs, authentication headers or referrers.
func FetchSyllabus(ctx context.Context, s *Syllabus) error {
	return fetchSyllabus(ctx, s, http.DefaultTransport)
}
func fetchSyllabus(ctx context.Context, s *Syllabus, transport http.RoundTripper) error {
	hc := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errRedirectLimit
		}
		if !sameOrigin(req.URL, mustURL(syllabusOrigin)) || req.URL.User != nil {
			return errUntrustedTarget
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, "GET", s.URL, nil)
	if err != nil {
		return errors.New("invalid syllabus URL")
	}
	if !sameOrigin(req.URL, mustURL(syllabusOrigin)) || req.URL.User != nil {
		return errUntrustedTarget
	}
	req.Header.Set("User-Agent", "YMCA/0.1 (YNU LMS CLI)")
	resp, err := hc.Do(req)
	if err != nil {
		return requestFailure(req, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("syllabus returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
	if err != nil {
		return requestFailure(req, err)
	}
	if len(body) > 8*1024*1024 {
		return errors.New("syllabus response exceeded 8 MiB")
	}
	return parseSyllabus(string(body), s)
}
func parseSyllabus(body string, s *Syllabus) error {
	d := document(body)
	suffix := func(end string) *html.Node {
		return first(d, func(n *html.Node) bool { return strings.HasSuffix(attr(n, "id"), end) })
	}
	code := text(suffix("sylSummary_txtLctCd"))
	title := text(suffix("sylSummary_txtSbjName"))
	if code != s.Code || title == "" {
		return errors.New("public syllabus was not found for this academic year and lecture code")
	}
	s.Title = title
	s.Fields = []SyllabusField{}
	s.Sections = []SyllabusField{}
	summary := suffix("sylSummary_tblHeader")
	for _, label := range nodes(summary, func(n *html.Node) bool { return n.Data == "span" && strings.Contains(attr(n, "id"), "_lbl") }) {
		id := attr(label, "id")
		idx := strings.LastIndex(id, "_lbl")
		if idx < 0 {
			continue
		}
		name := id[idx+4:]
		if name == "LctCd" || name == "JeName" {
			continue
		}
		value := byID(d, id[:idx]+"_txt"+name)
		if text(value) != "" {
			s.Fields = append(s.Fields, SyllabusField{Label: text(label), Content: paragraphText(value)})
		}
	}
	for _, header := range nodes(d, func(n *html.Node) bool {
		return n.Data == "span" && strings.Contains(attr(n, "id"), "ucSylContents_") && strings.HasSuffix(attr(n, "id"), "_lblHeader")
	}) {
		table := header.Parent
		for table != nil && table.Data != "table" {
			table = table.Parent
		}
		if table == nil {
			continue
		}
		label := text(header)
		content := paragraphText(table)
		content = strings.TrimSpace(strings.TrimPrefix(content, label))
		if content != "" {
			s.Sections = append(s.Sections, SyllabusField{Label: label, Content: content})
		}
	}
	return nil
}
