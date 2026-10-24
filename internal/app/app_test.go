package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"ymca/internal/lms"
)

func TestOfflineProfileJSON(t *testing.T) {
	var out, stderr bytes.Buffer
	c := New(&out, &stderr)
	c.SetArgs([]string{"profile", "show", "--file", "../lms/testdata/dashboard.html", "--json"})
	if e := c.ExecuteContext(context.Background()); e != nil {
		t.Fatal(e)
	}
	var profile profileResult
	if e := json.Unmarshal(out.Bytes(), &profile); e != nil {
		t.Fatal(e)
	}
	if profile.Verified || profile.Name != "Example Student" || profile.PreviousLogin == nil {
		t.Fatal(profile)
	}
	if stderr.Len() != 0 || strings.Contains(out.String(), "SID=") {
		t.Fatal("output polluted")
	}
}
func TestErrorsAndExitCodes(t *testing.T) {
	for _, args := range [][]string{{"--unknown"}, {"assignments", "show"}, {"materials", "download", "--all"}} {
		var out, errOut bytes.Buffer
		c := New(&out, &errOut)
		c.SetArgs(args)
		e := c.Execute()
		if ExitCode(e) != 2 {
			t.Fatalf("%v returned %v (%d)", args, e, ExitCode(e))
		}
	}
	var out, stderr bytes.Buffer
	c := New(&out, &stderr)
	c.SetArgs([]string{"--state-dir", t.TempDir(), "profile", "show"})
	if e := c.Execute(); ExitCode(e) != 3 {
		t.Fatalf("auth error: %v", e)
	}
}
func TestSanitizeError(t *testing.T) {
	s := SafeError(usageError{lms.ErrAuth})
	if s == "" {
		t.Fatal(s)
	}
	if safeText("hello\x1b[31m\x00") != "hello[31m" {
		t.Fatal("terminal controls retained")
	}
}

func TestZshCompletionDoesNotAccessLMS(t *testing.T) {
	var out, stderr bytes.Buffer
	c := New(&out, &stderr)
	c.SetArgs([]string{"--state-dir", t.TempDir(), "completion", "zsh"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "#compdef ymca") || stderr.Len() != 0 {
		t.Fatal("completion script was not generated")
	}
}
