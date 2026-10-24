package lms

import (
	"bytes"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

func document(s string) *html.Node { n, _ := html.Parse(strings.NewReader(s)); return n }
func attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}
func class(n *html.Node, c string) bool {
	for _, v := range strings.Fields(attr(n, "class")) {
		if v == c {
			return true
		}
	}
	return false
}
func nodes(n *html.Node, pred func(*html.Node) bool) []*html.Node {
	out := []*html.Node{}
	if n == nil {
		return out
	}
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && pred(x) {
			out = append(out, x)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}
func first(n *html.Node, pred func(*html.Node) bool) *html.Node {
	v := nodes(n, pred)
	if len(v) == 0 {
		return nil
	}
	return v[0]
}
func byClass(n *html.Node, c string) *html.Node {
	return first(n, func(x *html.Node) bool { return class(x, c) })
}
func byID(n *html.Node, id string) *html.Node {
	return first(n, func(x *html.Node) bool { return attr(x, "id") == id })
}
func text(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	if n.Data == "script" || n.Data == "style" {
		return ""
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(text(c))
		b.WriteByte(' ')
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
func directText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
func attributes(n *html.Node) map[string]string {
	m := map[string]string{}
	for _, a := range n.Attr {
		m[a.Key] = a.Val
	}
	return m
}
func collectForms(s string) []formData {
	out := []formData{}
	for _, n := range nodes(document(s), func(x *html.Node) bool { return x.Data == "form" }) {
		var body bytes.Buffer
		html.Render(&body, n)
		f := formData{attrs: attributes(n), body: body.String()}
		for _, x := range nodes(n, func(x *html.Node) bool { return x.Data == "input" }) {
			if !hasAttr(x, "disabled") {
				f.inputs = append(f.inputs, attributes(x))
			}
		}
		for _, x := range nodes(n, func(x *html.Node) bool { return x.Data == "button" }) {
			f.buttons = append(f.buttons, attributes(x))
		}
		for _, x := range nodes(n, func(x *html.Node) bool { return x.Data == "select" }) {
			sel := selectData{attrs: attributes(x)}
			for _, o := range nodes(x, func(x *html.Node) bool { return x.Data == "option" }) {
				a := attributes(o)
				if !hasAttr(o, "value") {
					a["value"] = text(o)
				}
				sel.options = append(sel.options, a)
			}
			f.selects = append(f.selects, sel)
		}
		out = append(out, f)
	}
	return out
}
func formValues(f *formData) url.Values {
	v := url.Values{}
	if f == nil {
		return v
	}
	for _, x := range f.inputs {
		t := strings.ToLower(x["type"])
		if x["name"] == "" || t == "submit" || t == "button" || t == "file" {
			continue
		}
		if t == "checkbox" || t == "radio" {
			if _, ok := x["checked"]; !ok {
				continue
			}
		}
		v.Add(x["name"], x["value"])
	}
	for _, s := range f.selects {
		if s.attrs["name"] != "" {
			v.Set(s.attrs["name"], selectedOptionValue(s))
		}
	}
	return v
}
