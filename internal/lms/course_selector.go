package lms

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var internalCourseID = regexp.MustCompile(`^KOG[A-Za-z0-9_-]+$`)
var lectureCode = regexp.MustCompile(`^[A-Za-z0-9]{7}$`)

func ValidCourseSelector(s string) bool {
	return internalCourseID.MatchString(s) || lectureCode.MatchString(s)
}

func (c *Client) rememberCourseCode(year, id, code string) {
	if c.CourseCodes == nil {
		c.CourseCodes = map[string]map[string]string{}
	}
	if c.CourseCodes[year] == nil {
		c.CourseCodes[year] = map[string]string{}
	}
	c.CourseCodes[year][id] = code
}

// ResolveCourse reads only course entry pages, not reports, attachments or
// lesson AJAX blocks. All current candidates are checked before selecting a
// code, since duplicate lecture codes must not silently choose a course.
func (c *Client) ResolveCourse(selector string, progress func(string)) (string, error) {
	if internalCourseID.MatchString(selector) && !lectureCode.MatchString(selector) {
		return selector, nil
	}
	if !lectureCode.MatchString(selector) {
		return "", errors.New("course must be an internal KOG ID or a seven-character lecture code")
	}
	p, err := c.dashboardPage()
	if err != nil {
		return "", err
	}
	d, err := ParseDashboard(p.Body)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	matches := []string{}
	var incomplete error
	for _, course := range d.Courses {
		if seen[course.ID] {
			continue
		}
		seen[course.ID] = true
		code, known := c.CourseCodes[d.Year][course.ID]
		if !known {
			if progress != nil {
				progress("Looking up lecture code: " + course.ID)
			}
			entry, e := c.homeSubmit(p, "formSubmit", url.Values{"kougiId": {course.ID}})
			if e == nil && byClass(document(entry.Body), "courseName") == nil {
				e = ErrStructure
			}
			if e != nil {
				if errors.Is(e, ErrAuth) || c.ctx.Err() != nil {
					return "", e
				}
				if incomplete == nil {
					incomplete = fmt.Errorf("course %s: %w", course.ID, e)
				}
				continue
			}
			code = courseCode(document(entry.Body))
			c.rememberCourseCode(d.Year, course.ID, code)
		}
		if strings.EqualFold(code, selector) {
			matches = append(matches, course.ID)
		}
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("lecture code %s matches multiple courses: %s; specify an internal KOG ID", selector, strings.Join(matches, ", "))
	}
	if incomplete != nil {
		return "", fmt.Errorf("lecture-code lookup is incomplete; retry or specify an internal KOG ID: %w", incomplete)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("lecture code %s was not found in the current LMS course list (%s)", selector, d.Year)
	}
	return matches[0], nil
}
