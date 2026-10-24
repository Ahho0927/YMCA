package state

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestConcurrentStateUpdates(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.WithLock(context.Background(), func(st *State) error {
				if st.Session == nil {
					st.Session = &Session{}
				}
				st.Session.LastActivity = st.Session.LastActivity.Add(time.Second)
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	err := s.WithLock(context.Background(), func(st *State) error {
		if !st.Session.LastActivity.Equal(time.Time{}.Add(12 * time.Second)) {
			t.Fatal("updates lost")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, e := os.Stat(filepath.Join(s.Dir, "state.json"))
		if e != nil {
			t.Fatal(e)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatal(info.Mode())
		}
		info, _ = os.Stat(s.Dir)
		if info.Mode().Perm() != 0700 {
			t.Fatal(info.Mode())
		}
	}
}
func TestCorruptStateIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	s := Store{Dir: dir}
	os.WriteFile(filepath.Join(dir, "state.json"), []byte("broken"), 0600)
	if e := s.WithLock(context.Background(), func(*State) error { return nil }); e == nil {
		t.Fatal("corrupt state accepted")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if string(b) != "broken" {
		t.Fatal("corrupt state overwritten")
	}
}
