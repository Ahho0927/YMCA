package lms

import (
	"fmt"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type NoticeDetail struct {
	Detail
	Type          string     `json:"type"`
	Sender        string     `json:"sender"`
	Published     *time.Time `json:"published"`
	Expires       *time.Time `json:"expires"`
	RelatedCourse string     `json:"related_course"`
	Attachments   []Material `json:"attachments"`
}

func labeledCells(n *html.Node) map[string]*html.Node {
	out := map[string]*html.Node{}
	for _, row := range nodes(n, func(x *html.Node) bool { return x.Data == "tr" }) {
		cells := directCells(row)
		for i := 0; i+1 < len(cells); i++ {
			if cells[i].Data == "th" && cells[i+1].Data == "td" {
				out[strings.TrimSuffix(text(cells[i]), ":")] = cells[i+1]
			}
		}
	}
	return out
}
func cellValue(cells map[string]*html.Node, labels ...string) *html.Node {
	for _, l := range labels {
		if n := cells[l]; n != nil {
			return n
		}
	}
	return nil
}
func ParseNoticePage(p Page, expectedID string) (*NoticeDetail, error) {
	doc := document(p.Body)
	form := findForm(collectForms(p.Body), "id", "infr_form")
	if form == nil {
		return nil, fmt.Errorf("%w: notice detail form missing", ErrStructure)
	}
	values := formValues(form)
	id := values.Get("infId")
	if id == "" || id != expectedID {
		return nil, fmt.Errorf("%w: notice ID mismatch", ErrStructure)
	}
	table := byClass(doc, "cs_table3a")
	if table == nil {
		return nil, ErrStructure
	}
	cells := labeledCells(table)
	title := text(cellValue(cells, "タイトル", "Title"))
	body := cellValue(cells, "内容", "Content")
	if title == "" || body == nil {
		return nil, ErrStructure
	}
	out := &NoticeDetail{Detail: Detail{ID: id, Title: title, Content: paragraphText(body)}, Type: values.Get("infType"), Sender: text(cellValue(cells, "発信者", "Sender")), Published: parseDate(text(cellValue(cells, "発信日時", "Sent"))), Expires: parseDate(text(cellValue(cells, "公開終了日時", "Publication end"))), RelatedCourse: text(cellValue(cells, "関連講義名", "Related course")), Attachments: []Material{}}
	if attachments := cellValue(cells, "添付ファイル", "Attachments"); attachments != nil {
		for _, a := range nodes(attachments, func(n *html.Node) bool { return n.Data == "a" }) {
			nav, err := downloadNavigation(p, a)
			if err != nil {
				return nil, err
			}
			id := nav.values.Get("fileId")
			if id == "" {
				id = stableResourceID(nav)
			}
			out.Attachments = append(out.Attachments, Material{ID: id, Title: text(a), request: nav})
		}
	}
	return out, nil
}
