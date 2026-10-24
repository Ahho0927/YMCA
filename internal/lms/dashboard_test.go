package lms

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	b, e := os.ReadFile("testdata/dashboard.html")
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestDashboard(t *testing.T) {
	d, e := ParseDashboard(fixture(t))
	if e != nil {
		t.Fatal(e)
	}
	if d.Account.Name != "Example Student" || d.Account.Role != "受講者" {
		t.Fatalf("account: %+v", d.Account)
	}
	if d.Account.PreviousLogin == nil || d.Account.PreviousLogin.Format("2006-01-02T15:04:05Z07:00") != "2025-04-10T12:30:00+09:00" {
		t.Fatalf("date: %+v", d.Account)
	}
	if d.Year != "2025" || strings.Join(d.SelectedTerms, ",") != "01,02" {
		t.Fatalf("filters: %+v", d)
	}
	if len(d.Courses) != 4 || d.Courses[0].Day != "月曜日" || d.Courses[1].Period != "オンデマンド" || !d.Courses[0].Provisional || d.Courses[3].Term != "通年" {
		t.Fatalf("courses: %+v", d.Courses)
	}
	if d.Courses[0].ID == d.Courses[1].ID || d.Courses[0].Name != d.Courses[1].Name {
		t.Fatal("same-title courses were conflated")
	}
	if len(d.Notices) != 1 || d.Notices[0].Type != "03" || len(d.Topics) != 1 {
		t.Fatalf("notices/topics: %+v", d)
	}
}
func TestDashboardEmptyAndInvalid(t *testing.T) {
	for _, input := range []string{"<html>maintenance</html>", "<div class=weeklyCourseArea></div>"} {
		if _, e := ParseDashboard(input); !errors.Is(e, ErrStructure) {
			t.Fatalf("expected structure error, got %v", e)
		}
	}
	if _, e := ParseDashboard(`<form id="gakuninLoginForm"></form>`); !errors.Is(e, ErrAuth) {
		t.Fatal(e)
	}
	d, e := ParseDashboard(`<form id=homeHomlForm></form><div class=homeProfName>Student</div><div class=weeklyCourseArea></div>`)
	if e != nil || d.Courses == nil || len(d.Courses) != 0 {
		t.Fatalf("empty dashboard: %+v, %v", d, e)
	}
}
