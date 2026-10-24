// Package keepalive implements a scheduler without system wake or sleep assertions.
package keepalive

import (
	"context"
	"errors"
	"time"

	"ymca/internal/lms"
	"ymca/internal/state"
)

const Interval = time.Hour
const Poll = 30 * time.Second

type Engine struct {
	Store    state.Store
	Probe    func(context.Context, *state.Session) (*state.Session, error)
	lastTick time.Time
	retry    int
}

func (e *Engine) Tick(ctx context.Context, now time.Time) error {
	resumed := !e.lastTick.IsZero() && now.Sub(e.lastTick) > 2*Poll
	first := e.lastTick.IsZero()
	e.lastTick = now
	return e.Store.WithLock(ctx, func(s *state.State) error {
		k := &s.Keepalive
		if !k.Enabled {
			return nil
		}
		k.Heartbeat = now
		if s.Session == nil || k.NeedsLogin {
			return nil
		}
		due := s.Session.LastActivity.Add(Interval)
		if e.retry > 0 && !k.NextAttempt.IsZero() {
			due = k.NextAttempt
		}
		if !first && !resumed && now.Before(due) {
			k.NextAttempt = due
			return nil
		}
		session, err := e.Probe(ctx, s.Session)
		if errors.Is(err, lms.ErrAuth) {
			k.NeedsLogin = true
			k.LastError = lms.ErrAuth.Error()
			k.NextAttempt = time.Time{}
			return nil
		}
		if err != nil {
			e.retry++
			// Two short retries, then the next regular interval. Never send credentials.
			delay := Interval
			if e.retry == 1 {
				delay = time.Minute
			}
			if e.retry == 2 {
				delay = 5 * time.Minute
			}
			k.NextAttempt = now.Add(delay)
			k.LastError = err.Error()
			return nil
		}
		e.retry = 0
		session.LastActivity = now
		s.Session = session
		k.LastSuccess = now
		k.LastError = ""
		k.NextAttempt = now.Add(Interval)
		return nil
	})
}
func (e *Engine) Run(ctx context.Context) error {
	if err := e.Tick(ctx, time.Now().UTC()); err != nil {
		return err
	}
	ticker := time.NewTicker(Poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			enabled := false
			if err := e.Store.WithLock(ctx, func(s *state.State) error { enabled = s.Keepalive.Enabled; return nil }); err != nil {
				return err
			}
			if !enabled {
				return nil
			}
			// Use wall clock, not the ticker's potentially buffered pre-suspend timestamp.
			if err := e.Tick(ctx, time.Now().UTC()); err != nil {
				return err
			}
		}
	}
}

type Status struct {
	Enabled     bool       `json:"enabled"`
	Running     bool       `json:"running"`
	NeedsLogin  bool       `json:"needs_login"`
	LastSuccess *time.Time `json:"last_success"`
	NextAttempt *time.Time `json:"next_attempt"`
	LastError   string     `json:"last_error,omitempty"`
}

func Describe(k state.Keepalive, now time.Time) Status {
	s := Status{Enabled: k.Enabled, Running: k.Enabled && !k.Heartbeat.IsZero() && now.Sub(k.Heartbeat) < 3*Poll, NeedsLogin: k.NeedsLogin, LastError: k.LastError}
	if !k.LastSuccess.IsZero() {
		v := k.LastSuccess.In(time.FixedZone("JST", 9*3600))
		s.LastSuccess = &v
	}
	if !k.NextAttempt.IsZero() {
		v := k.NextAttempt.In(time.FixedZone("JST", 9*3600))
		s.NextAttempt = &v
	}
	return s
}
