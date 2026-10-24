// Package service installs only per-user background jobs; never changes power settings.
package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"ymca/internal/state"
)

const label = "org.ymca.keepalive"

func xmlText(s string) string { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }
func launchAgent(executable, dir string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>` + label + `</string><key>ProgramArguments</key><array><string>` + xmlText(executable) + `</string><string>--state-dir</string><string>` + xmlText(dir) + `</string><string>keepalive</string><string>run</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict><key>ThrottleInterval</key><integer>30</integer><key>ProcessType</key><string>Background</string></dict></plist>`
}
func systemdQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`, "\n", `\n`).Replace(s) + `"`
}
func unit(executable, dir string) string {
	return "[Unit]\nDescription=YMCA LMS session keepalive\n[Service]\nExecStart=" + systemdQuote(executable) + " --state-dir " + systemdQuote(dir) + " keepalive run\nRestart=on-failure\nRestartSec=30\n[Install]\nWantedBy=default.target\n"
}
func windowsQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		if r == '\\' {
			slashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat(`\`, slashes*2+1))
		} else {
			b.WriteString(strings.Repeat(`\`, slashes))
		}
		slashes = 0
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat(`\`, slashes*2))
	b.WriteByte('"')
	return b.String()
}
func task(executable, dir, userID string) string {
	args := `--state-dir ` + windowsQuote(dir) + ` keepalive run`
	return `<?xml version="1.0" encoding="UTF-8"?><Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + xmlText(userID) + `</UserId></LogonTrigger></Triggers><Principals><Principal id="Author"><UserId>` + xmlText(userID) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><WakeToRun>false</WakeToRun><RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure></Settings><Actions Context="Author"><Exec><Command>` + xmlText(executable) + `</Command><Arguments>` + xmlText(args) + `</Arguments></Exec></Actions></Task>`
}
func paths() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		h, e := os.UserHomeDir()
		return filepath.Join(h, "Library", "LaunchAgents", label+".plist"), e
	case "linux":
		d, e := os.UserConfigDir()
		return filepath.Join(d, "systemd", "user", "ymca-keepalive.service"), e
	case "windows":
		return "", nil
	}
	return "", errors.New("background installation is unsupported; use 'ymca keepalive run'")
}
func run(name string, args ...string) error {
	if err := exec.Command(name, args...).Run(); err != nil {
		return fmt.Errorf("%s background-service command failed; use 'ymca keepalive run' or inspect your OS service manager", name)
	}
	return nil
}
func Install(executable, dir string) error {
	if strings.ContainsAny(executable+dir, "\r\n\x00") {
		return errors.New("invalid service path")
	}
	path, err := paths()
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		u, err := user.Current()
		if err != nil {
			return err
		}
		path = filepath.Join(dir, "keepalive-task.xml")
		data := task(executable, dir, u.Uid)
		if err = state.AtomicWrite(path, []byte(data)); err != nil {
			return err
		}
		defer os.Remove(path)
		if err = run("schtasks", "/Create", "/TN", label, "/XML", path, "/F"); err != nil {
			return err
		}
		return run("schtasks", "/Run", "/TN", label)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data := unit(executable, dir)
	if runtime.GOOS == "darwin" {
		data = launchAgent(executable, dir)
	}
	if err = state.AtomicWrite(path, []byte(data)); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		domain := "gui/" + strconv.Itoa(os.Getuid())
		_ = exec.Command("launchctl", "bootout", domain+"/"+label).Run()
		return run("launchctl", "bootstrap", domain, path)
	}
	if err = run("systemctl", "--user", "daemon-reload"); err != nil {
		return err
	}
	return run("systemctl", "--user", "enable", "--now", "ymca-keepalive.service")
}
func Remove() error {
	path, err := paths()
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		// Query first to make repeated disable/logout harmless.
		if exec.Command("schtasks", "/Query", "/TN", label).Run() != nil {
			return nil
		}
		_ = exec.Command("schtasks", "/End", "/TN", label).Run()
		return run("schtasks", "/Delete", "/TN", label, "/F")
	}
	if _, err = os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		_ = exec.Command("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+label).Run()
	} else {
		if err = run("systemctl", "--user", "disable", "--now", "ymca-keepalive.service"); err != nil {
			return err
		}
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		return run("systemctl", "--user", "daemon-reload")
	}
	return nil
}
