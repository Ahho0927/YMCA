// Package state owns private, process-locked authentication and worker state.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/gofrs/flock"
)

type Cookie struct {
	URL    string      `json:"url"`
	Cookie http.Cookie `json:"cookie"`
}
type Session struct {
	SID          string    `json:"sid"`
	Cookies      []Cookie  `json:"cookies"`
	LastActivity time.Time `json:"last_activity"`
}
type Keepalive struct {
	Enabled     bool      `json:"enabled"`
	Heartbeat   time.Time `json:"heartbeat,omitempty"`
	LastSuccess time.Time `json:"last_success,omitempty"`
	NextAttempt time.Time `json:"next_attempt,omitempty"`
	NeedsLogin  bool      `json:"needs_login"`
	LastError   string    `json:"last_error,omitempty"`
}
type State struct {
	Session   *Session  `json:"session,omitempty"`
	Keepalive Keepalive `json:"keepalive"`
	// Course codes are scoped to this login and academic year. No authentication
	// values belong in this cache; a new login clears it, including account changes.
	CourseCodes map[string]map[string]string `json:"course_codes,omitempty"`
}
type Store struct{ Dir string }

func DefaultDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "YMCA"), err
	case "windows":
		dir, err := os.UserConfigDir()
		return filepath.Join(dir, "YMCA"), err
	default:
		if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
			return filepath.Join(dir, "ymca"), nil
		}
		home, err := os.UserHomeDir()
		return filepath.Join(home, ".local", "share", "ymca"), err
	}
}
func (s Store) Prepare() error {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	return protectDir(s.Dir)
}
func (s Store) WithLock(ctx context.Context, fn func(*State) error) error {
	if err := s.Prepare(); err != nil {
		return err
	}
	lock := flock.New(filepath.Join(s.Dir, "state.lock"))
	ok, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("session is busy")
	}
	defer lock.Close()
	st := State{}
	data, err := os.ReadFile(filepath.Join(s.Dir, "state.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(data) > 0 {
		if err = json.Unmarshal(data, &st); err != nil {
			return errors.New("session state is damaged; move the state file aside and log in again")
		}
	}
	callErr := fn(&st)
	if err = s.write(st); err != nil {
		return fmt.Errorf("save session state: %w", err)
	}
	return callErr
}
func (s Store) write(st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(filepath.Join(s.Dir, "state.json"), data)
}
func AtomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".ymca-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
