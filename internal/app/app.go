package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"ymca/internal/keepalive"
	"ymca/internal/lms"
	"ymca/internal/service"
	"ymca/internal/state"
)

var Version = "dev"

type usageError struct{ error }
type App struct {
	store        state.Store
	json         bool
	captureDir   string
	out, err     io.Writer
	captureCount int
}

func New(out, errOut io.Writer) *cobra.Command {
	dir, _ := state.DefaultDir()
	a := &App{store: state.Store{Dir: dir}, out: out, err: errOut}
	root := &cobra.Command{Use: "ymca", Short: "YNU LMS Management CLI Assistant", Version: Version, SilenceErrors: true, SilenceUsage: true, Args: cobra.NoArgs}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetFlagErrorFunc(func(_ *cobra.Command, e error) error { return usageError{e} })
	a.globalFlags(root)
	root.RunE = showGroupHelp
	a.homeCommands(root)
	a.authCommands(root)
	a.keepaliveCommands(root)
	a.resourceCommands(root)
	root.SetHelpCommand(&cobra.Command{Use: "help [command]", Short: "Help about any command", RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			rest := append(append([]string{}, args[1:]...), "--help")
			switch args[0] {
			case "courses":
				return a.routeCourses(cmd, rest)
			case "notices":
				return a.routeNotices(cmd, rest)
			}
		}
		target, _, err := root.Find(args)
		if err != nil {
			return usageError{err}
		}
		return target.Help()
	}})
	return root
}
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, lms.ErrAuth) {
		return 3
	}
	var u usageError
	if errors.As(err, &u) {
		return 2
	}
	if strings.HasPrefix(err.Error(), "unknown command") || strings.HasPrefix(err.Error(), "accepts ") || strings.Contains(err.Error(), "arg(s)") {
		return 2
	}
	return 1
}
func SafeError(err error) string {
	// URLs from authentication errors must never expose SID, SAML, authId or tokens.
	s := regexp.MustCompile(`https?://[^\s]+`).ReplaceAllString(err.Error(), "[URL omitted]")
	s = regexp.MustCompile(`(?i)(;SID=|tokenId[=: ]+|SAMLResponse[=: ]+)[^\s]+`).ReplaceAllString(s, "[secret omitted]")
	return safeText(s)
}
func (a *App) client(ctx context.Context, s *state.Session) *lms.Client {
	c := lms.New(ctx, s)
	if a.captureDir != "" {
		c.Capture = func(path string, b []byte) error {
			dir, err := filepath.Abs(a.captureDir)
			if err != nil {
				return err
			}
			if err = (state.Store{Dir: dir}).Prepare(); err != nil {
				return err
			}
			a.captureCount++
			path = strings.Split(path, ";")[0]
			name := regexp.MustCompile(`[^a-zA-Z0-9_-]`).ReplaceAllString(path, "_")
			return state.AtomicWrite(filepath.Join(dir, fmt.Sprintf("%s-%s-%03d.html", name, time.Now().UTC().Format("20060102T150405.000000000"), a.captureCount)), b)
		}
	}
	return c
}
func (a *App) withClient(ctx context.Context, fn func(*lms.Client) error) error {
	return a.store.WithLock(ctx, func(s *state.State) error {
		if s.Session == nil || s.Keepalive.NeedsLogin {
			return lms.ErrAuth
		}
		c := a.client(ctx, s.Session)
		if s.CourseCodes == nil {
			s.CourseCodes = map[string]map[string]string{}
		}
		c.CourseCodes = s.CourseCodes
		err := fn(c)
		if errors.Is(err, lms.ErrAuth) {
			s.Keepalive.NeedsLogin = true
			s.Keepalive.LastError = lms.ErrAuth.Error()
			s.Keepalive.NextAttempt = time.Time{}
			return err
		}
		if !c.LastActivity().IsZero() && c.LastActivity().After(s.Session.LastActivity) {
			s.Session = c.Snapshot()
			s.Keepalive.NeedsLogin = false
			if s.Keepalive.Enabled {
				s.Keepalive.NextAttempt = s.Session.LastActivity.Add(keepalive.Interval)
			}
		}
		return err
	})
}
func (a *App) authCommands(root *cobra.Command) {
	auth := &cobra.Command{Use: "auth", Short: "Manage the local LMS session"}
	root.AddCommand(auth)
	login := &cobra.Command{Use: "login", Short: "Log in using your YNU account and optional email code", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("login requires an interactive terminal; passwords and codes cannot be passed as arguments")
		}
		reader := bufio.NewReader(os.Stdin)
		fmt.Fprint(a.err, "YNU username: ")
		username, e := reader.ReadString('\n')
		if e != nil {
			return e
		}
		username = strings.TrimSpace(username)
		if username == "" {
			return usageError{errors.New("username is required")}
		}
		password, e := a.secret("YNU password: ")
		if e != nil {
			return e
		}
		var d *lms.Dashboard
		e = a.store.WithLock(cmd.Context(), func(s *state.State) error {
			c := a.client(cmd.Context(), nil)
			var err error
			d, err = c.Login(username, password, func() (string, error) { return a.secret("YNU email 2FA code: ") })
			password = ""
			if err != nil {
				return err
			}
			s.Session = c.Snapshot()
			s.CourseCodes = nil
			s.Keepalive.NeedsLogin = false
			s.Keepalive.LastError = ""
			if s.Keepalive.Enabled {
				s.Keepalive.NextAttempt = s.Session.LastActivity.Add(keepalive.Interval)
			}
			return nil
		})
		if e != nil {
			return e
		}
		return a.print(map[string]any{"authenticated": true, "account": d.Account})
	}}
	auth.AddCommand(login)
	auth.AddCommand(&cobra.Command{Use: "status", Short: "Verify the saved session with LMS", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		err := a.withClient(cmd.Context(), func(c *lms.Client) error { _, e := c.Dashboard(); return e })
		if err != nil {
			return err
		}
		return a.print(map[string]any{"authenticated": true})
	}})
	auth.AddCommand(&cobra.Command{Use: "logout", Short: "Delete local credentials and stop YMCA keepalive", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := a.store.WithLock(cmd.Context(), func(s *state.State) error { *s = state.State{}; return nil }); err != nil {
			return err
		}
		if err := service.Remove(); err != nil {
			return err
		}
		return a.print(map[string]any{"authenticated": false, "keepalive_enabled": false})
	}})
}
func (a *App) secret(label string) (string, error) {
	fmt.Fprint(a.err, label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(a.err)
	s := string(b)
	for i := range b {
		b[i] = 0
	}
	if err != nil {
		return "", err
	}
	if s == "" {
		return "", errors.New("empty secret input")
	}
	return s, nil
}
func (a *App) keepaliveCommands(root *cobra.Command) {
	group := &cobra.Command{Use: "keepalive", Short: "Opt in to session maintenance without waking your laptop"}
	root.AddCommand(group)
	group.AddCommand(&cobra.Command{Use: "status", Short: "Show background worker and session-maintenance status", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return a.store.WithLock(cmd.Context(), func(s *state.State) error { return a.print(keepalive.Describe(s.Keepalive, time.Now())) })
	}})
	group.AddCommand(&cobra.Command{Use: "enable", Short: "Install and start a per-user background worker", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return err
		}
		if strings.Contains(exe, "go-build") {
			return errors.New("build/install a permanent ymca executable before enabling the background worker")
		}
		a.store.Dir, err = filepath.Abs(a.store.Dir)
		if err != nil {
			return err
		}
		err = a.store.WithLock(cmd.Context(), func(s *state.State) error {
			if s.Session == nil || s.Keepalive.NeedsLogin {
				return lms.ErrAuth
			}
			s.Keepalive.Enabled = true
			return nil
		})
		if err != nil {
			return err
		}
		if err = service.Install(exe, a.store.Dir); err != nil {
			_ = a.store.WithLock(cmd.Context(), func(s *state.State) error { s.Keepalive.Enabled = false; return nil })
			_ = service.Remove()
			return err
		}
		return a.print(map[string]any{"enabled": true, "interval_minutes": 60, "wakes_computer": false})
	}})
	group.AddCommand(&cobra.Command{Use: "disable", Short: "Stop keepalive and remove its per-user job", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := a.store.WithLock(cmd.Context(), func(s *state.State) error {
			s.Keepalive.Enabled = false
			s.Keepalive.Heartbeat = time.Time{}
			s.Keepalive.NextAttempt = time.Time{}
			return nil
		}); err != nil {
			return err
		}
		if err := service.Remove(); err != nil {
			return err
		}
		return a.print(map[string]any{"enabled": false})
	}})
	group.AddCommand(&cobra.Command{Use: "run", Short: "Run the session-maintenance worker in the foreground", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := a.store.Prepare(); err != nil {
			return err
		}
		lock := flock.New(filepath.Join(a.store.Dir, "worker.lock"))
		ok, err := lock.TryLock()
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("a keepalive worker is already running")
		}
		defer lock.Close()
		err = a.store.WithLock(cmd.Context(), func(s *state.State) error {
			if s.Session == nil {
				return lms.ErrAuth
			}
			s.Keepalive.Enabled = true
			return nil
		})
		if err != nil {
			return err
		}
		defer a.store.WithLock(context.Background(), func(s *state.State) error { s.Keepalive.Heartbeat = time.Time{}; return nil })
		engine := keepalive.Engine{Store: a.store, Probe: func(ctx context.Context, s *state.Session) (*state.Session, error) {
			c := lms.New(ctx, s)
			if _, e := c.Dashboard(); e != nil {
				return nil, e
			}
			return c.Snapshot(), nil
		}}
		return engine.Run(cmd.Context())
	}})
}
func safeText(s string) string {
	return strings.Map(func(r rune) rune {
		if (r < 32 && r != '\n' && r != '\t') || r == 127 || r == 0x1b {
			return -1
		}
		return r
	}, s)
}
func (a *App) print(v any) error {
	if a.json {
		enc := json.NewEncoder(a.out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	return a.human(v)
}
func (a *App) line(format string, args ...any) {
	width := 100
	if term.IsTerminal(int(os.Stdout.Fd())) {
		if w, _, e := term.GetSize(int(os.Stdout.Fd())); e == nil && w > 10 {
			width = w
		}
	}
	fmt.Fprintln(a.out, runewidth.Wrap(safeText(fmt.Sprintf(format, args...)), width))
}
func (a *App) human(v any) error {
	switch d := v.(type) {
	case []any:
		for _, item := range d {
			if err := a.human(item); err != nil {
				return err
			}
		}
	case lms.Material:
		return a.human([]lms.Material{d})
	case lms.Assignment:
		return a.human(&d)
	case profileResult:
		status := "offline HTML (session not verified)"
		if d.Verified {
			status = "authenticated"
		}
		a.line("%s · %s · %s", d.Name, d.Role, status)
		if d.PreviousLogin != nil {
			a.line("Previous login: %s", d.PreviousLogin.Format("2006-01-02 15:04 MST"))
		}
	case []lms.Topic:
		for _, topic := range d {
			a.line("%s  %s  [%s]", date(topic.Date), topic.Title, topic.ID)
		}
	case []lms.Course:
		day := ""
		for _, c := range d {
			if day != c.Day {
				day = c.Day
				a.line("\n%s", day)
			}
			flag := ""
			if c.Provisional {
				flag = " · provisional"
			}
			id := c.ID
			if c.Code != "" {
				id += " · " + c.Code
			}
			a.line("%s · %s · %s%s\n  %s\n  %s", c.Period, c.Term, c.Name, flag, c.Teacher, id)
		}
	case []lms.Notice:
		for _, n := range d {
			a.line("%s  %s  [%s]", date(n.Date), n.Title, n.ID)
		}
	case *lms.Detail:
		a.line("%s [%s]\n\n%s", d.Title, d.ID, d.Content)
	case *lms.NoticeDetail:
		a.line("%s [%s]\nSender: %s · Published: %s\nCourse: %s\n\n%s", d.Title, d.ID, d.Sender, date(d.Published), d.RelatedCourse, d.Content)
		if len(d.Attachments) > 0 {
			a.line("\nAttachments")
			a.human(d.Attachments)
		}
	case *lms.MaterialDetail:
		a.line("%s [%s]\n\n%s", d.Material.Title, d.Material.ID, d.Content)
		if len(d.Files) > 0 {
			a.line("\nFiles")
			a.human(d.Files)
		}
	case *lms.CourseDetail:
		a.line("%s [%s]", d.Title, d.ID)
		if d.Course.Code != "" {
			a.line("Lecture code: %s · Academic year: %s", d.Course.Code, d.Course.Year)
		}
		for _, lesson := range d.Lessons {
			a.line("\n%s · %s · %s", lesson.Name, date(lesson.Date), lesson.Attendance)
			if lesson.Description != "" {
				a.line("%s", lesson.Description)
			}
			a.line("  Materials: %d · Reports: %d · Other: %d", countItems(lesson, "01"), countItems(lesson, "02"), len(lesson.Items)-countItems(lesson, "01")-countItems(lesson, "02"))
		}
	case *lms.Syllabus:
		a.line("%s · %s · %s\n%s", d.Title, d.Code, d.Year, d.URL)
		for _, field := range d.Fields {
			a.line("%s: %s", field.Label, field.Content)
		}
		for _, section := range d.Sections {
			a.line("\n%s\n%s", section.Label, section.Content)
		}
	case []lms.Material:
		for _, m := range d {
			a.line("%s  %s\n  %s · %s", m.ID, m.Title, m.Section, m.Status)
		}
	case *lms.Assignment:
		deadline := "unknown"
		if d.Deadline != nil {
			deadline = d.Deadline.Format("2006-01-02 15:04 MST")
		}
		a.line("%s [%s]\nDue: %s · Status: %s\nCourse: %s · %s\n\n%s", d.Title, d.ID, deadline, d.SubmissionStatus, d.CourseID, d.Section, d.Content)
		if d.Deadline == nil && d.SubmissionPeriod != "" {
			a.line("Submission period (LMS): %s", d.SubmissionPeriod)
		}
	case []lms.Assignment:
		for _, v := range d {
			deadline := "unknown"
			if v.Deadline != nil {
				deadline = v.Deadline.Format("2006-01-02 15:04 MST")
			}
			a.line("%s · %s\n  Due: %s · Status: %s\n  %s · %s", v.ID, v.Title, deadline, v.SubmissionStatus, v.CourseID, v.Section)
			if v.Deadline == nil && v.SubmissionPeriod != "" {
				a.line("  Submission period (LMS): %s", v.SubmissionPeriod)
			}
		}
	case keepalive.Status:
		a.line("Enabled: %t · Running: %t · Needs login: %t", d.Enabled, d.Running, d.NeedsLogin)
		if d.LastSuccess != nil {
			a.line("Last success: %s", d.LastSuccess.Format(time.RFC3339))
		}
		if d.NextAttempt != nil {
			a.line("Next attempt: %s", d.NextAttempt.Format(time.RFC3339))
		}
		if d.LastError != "" {
			a.line("Last error: %s", d.LastError)
		}
	default:
		b, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			return e
		}
		a.line("%s", b)
	}
	return nil
}
func date(t *time.Time) string {
	if t == nil {
		return "unknown"
	}
	return t.Format("2006-01-02")
}

func countItems(lesson lms.Lesson, typeCode string) int {
	n := 0
	for _, item := range lesson.Items {
		if item.TypeCode == typeCode {
			n++
		}
	}
	return n
}
