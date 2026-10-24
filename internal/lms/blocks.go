package lms

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// CoursePower's public common.js posts form.serialize() and replaces the
// selected div. Only literal block/form selectors and discovered actions are
// supported here; this never evaluates arbitrary JavaScript.
func (c *Client) loadBlocks(p Page) (Page, error) {
	re := regexp.MustCompile(`loadBlock\(\s*['"]#([^'"]+)['"]\s*,\s*['"]#([^'"]+)['"](?:\s*,\s*['"]([^'"]+)['"])?`)
	calls := re.FindAllStringSubmatch(p.Body, -1)
	if len(calls) == 0 {
		return p, nil
	}
	if len(calls) > 8 {
		return Page{}, fmt.Errorf("%w: too many dynamic page blocks", ErrStructure)
	}
	doc := document(p.Body)
	loaded := map[string]bool{}
	for _, call := range calls {
		if loaded[call[1]] {
			continue
		}
		loaded[call[1]] = true
		target := byID(doc, call[1])
		if target == nil {
			continue
		}
		// Embedded course snapshots can already contain the populated block.
		// We still load it: this is the browser's observed on-load behavior.
		f := findForm(collectForms(p.Body), "id", call[2])
		if f == nil {
			return Page{}, fmt.Errorf("%w: dynamic block form missing", ErrStructure)
		}
		action := call[3]
		if action == "" {
			action = f.attrs["action"]
		}
		if action == "" || unsafeAction(action) {
			return Page{}, ErrStructure
		}
		fragment, err := c.readHTML("POST", absoluteURL(p.URL, action), formValues(f), true)
		if err != nil {
			return Page{}, err
		}
		children, err := html.ParseFragment(strings.NewReader(fragment.Body), target)
		if err != nil {
			return Page{}, ErrStructure
		}
		for target.FirstChild != nil {
			target.RemoveChild(target.FirstChild)
		}
		for _, child := range children {
			target.AppendChild(child)
		}
	}
	var b bytes.Buffer
	if err := html.Render(&b, doc); err != nil {
		return Page{}, err
	}
	p.Body = b.String()
	return p, nil
}
