package keepalive

import (
	"context"
	"errors"
	"testing"
	"time"
	"ymca/internal/lms"
	"ymca/internal/state"
)

func TestScheduling(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := state.Store{Dir: t.TempDir()}
	store.WithLock(ctx, func(s *state.State) error {
		s.Session = &state.Session{SID: "fixture", LastActivity: now}
		s.Keepalive.Enabled = true
		return nil
	})
	calls := 0
	e := Engine{Store: store, Probe: func(context.Context, *state.Session) (*state.Session, error) {
		calls++
		return &state.Session{SID: "fixture"}, nil
	}}
	if err := e.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("startup did not verify")
	}
	for i := 1; i < 120; i++ {
		if err := e.Tick(ctx, now.Add(time.Duration(i)*Poll)); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("unnecessary requests: %d", calls)
	}
	e.Tick(ctx, now.Add(time.Hour))
	if calls != 2 {
		t.Fatal("hourly request missing")
	}
	// A real command resets last activity and postpones the keepalive.
	store.WithLock(ctx, func(s *state.State) error { s.Session.LastActivity = now.Add(time.Hour + 30*time.Minute); return nil })
	for i := 121; i < 300; i++ {
		e.Tick(ctx, now.Add(time.Duration(i)*Poll))
	}
	if calls != 2 {
		t.Fatal("normal activity was ignored")
	}
	e.Tick(ctx, now.Add(150*time.Minute))
	if calls != 3 {
		t.Fatal("deferred request missing")
	}
}
func TestResumeAndExpiredSession(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store := state.Store{Dir: t.TempDir()}
	store.WithLock(ctx, func(s *state.State) error {
		s.Session = &state.Session{SID: "fixture", LastActivity: now}
		s.Keepalive.Enabled = true
		return nil
	})
	calls := 0
	expired := false
	e := Engine{Store: store, Probe: func(context.Context, *state.Session) (*state.Session, error) {
		calls++
		if expired {
			return nil, lms.ErrAuth
		}
		return &state.Session{SID: "fixture"}, nil
	}}
	e.Tick(ctx, now)
	expired = true
	e.Tick(ctx, now.Add(3*time.Hour))
	if calls != 2 {
		t.Fatal("resume did not verify")
	}
	e.Tick(ctx, now.Add(4*time.Hour))
	if calls != 2 {
		t.Fatal("expired session was retried")
	}
	store.WithLock(ctx, func(s *state.State) error {
		if !s.Keepalive.NeedsLogin || !s.Keepalive.NextAttempt.IsZero() {
			t.Fatal("expiration status incorrect")
		}
		s.Keepalive.NeedsLogin = false
		s.Session.LastActivity = now
		return nil
	})
	expired = false
	e.Tick(ctx, now.Add(4*time.Hour+Poll))
	if calls != 3 {
		t.Fatal("new login did not resume worker")
	}
}
func TestNetworkRetriesAndDisable(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	store := state.Store{Dir: t.TempDir()}
	store.WithLock(ctx, func(s *state.State) error {
		s.Session = &state.Session{SID: "fixture"}
		s.Keepalive.Enabled = true
		return nil
	})
	calls := 0
	e := Engine{Store: store, Probe: func(context.Context, *state.Session) (*state.Session, error) {
		calls++
		return nil, errors.New("offline")
	}}
	e.Tick(ctx, now)
	e.Tick(ctx, now.Add(Poll))
	if calls != 1 {
		t.Fatal("retried too soon")
	}
	e.Tick(ctx, now.Add(time.Minute))
	if calls != 2 {
		t.Fatal("first retry missing")
	}
	for i := 3; i <= 12; i++ {
		e.Tick(ctx, now.Add(time.Duration(i)*Poll))
	}
	if calls != 3 {
		t.Fatal("second retry missing")
	}
	store.WithLock(ctx, func(s *state.State) error {
		if s.Keepalive.NeedsLogin {
			t.Fatal("network error marked expired")
		}
		s.Keepalive.Enabled = false
		return nil
	})
	e.Tick(ctx, now.Add(2*time.Hour))
	if calls != 3 {
		t.Fatal("disabled worker sent request")
	}
}
