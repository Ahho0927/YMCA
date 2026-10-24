package service

import (
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServiceDefinitions(t *testing.T) {
	for _, s := range []string{launchAgent("/a b/ymca", "/a & b/state"), task(`C:\Program Files\YMCA\ymca.exe`, `C:\Users\User Name\YMCA`, "S-1-example")} {
		dec := xml.NewDecoder(strings.NewReader(s))
		for {
			_, err := dec.Token()
			if err != nil {
				if err.Error() != "EOF" {
					t.Fatal(err)
				}
				break
			}
		}
		if strings.Contains(s, "WakeToRun>true") || strings.Contains(s, "caffeinate") {
			t.Fatal("service wakes computer")
		}
	}
	u := unit("/a b/ymca", "/state/%x")
	if !strings.Contains(u, `"/a b/ymca"`) || !strings.Contains(u, "%%x") {
		t.Fatal(u)
	}
}

func TestWindowsArgumentQuoting(t *testing.T) {
	if got := windowsQuote(`C:\Users\User Name\YMCA`); got != `"C:\Users\User Name\YMCA"` {
		t.Fatal(got)
	}
	if got := windowsQuote(`C:\test\`); got != `"C:\test\\"` {
		t.Fatal(got)
	}
}

func TestLaunchAgentPlist(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS plist validation")
	}
	path := filepath.Join(t.TempDir(), "agent.plist")
	if err := os.WriteFile(path, []byte(launchAgent("/example/ymca", "/example/state")), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("plutil", "-lint", "--", path).CombinedOutput(); err != nil {
		t.Fatalf("plist validation: %s", output)
	}
}
