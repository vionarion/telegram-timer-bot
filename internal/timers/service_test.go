package timers

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vionarion/telegram-timer-bot/internal/store"
)

type fakeNotifier struct {
	mu    sync.Mutex
	fired []store.Timer
	late  []time.Duration
	ch    chan struct{}
}

func newFake() *fakeNotifier { return &fakeNotifier{ch: make(chan struct{}, 100)} }

func (f *fakeNotifier) TimerFired(_ context.Context, t store.Timer, late time.Duration) {
	f.mu.Lock()
	f.fired = append(f.fired, t)
	f.late = append(f.late, late)
	f.mu.Unlock()
	f.ch <- struct{}{}
}

func (f *fakeNotifier) wait(t *testing.T) {
	t.Helper()
	select {
	case <-f.ch:
	case <-time.After(3 * time.Second):
		t.Fatal("timer did not fire")
	}
}

func openStore(t *testing.T, path string) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// run starts the service and returns a function that stops it.
func run(t *testing.T, s *Service) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Run(ctx); close(done) }()
	// wait until Run has set the context
	for i := 0; i < 100; i++ {
		s.mu.Lock()
		ready := s.ctx != nil
		s.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	return func() { cancel(); <-done }
}

func TestTimerFiresAndIsDeleted(t *testing.T) {
	st := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	n := newFake()
	s := New(st, n)
	stop := run(t, s)
	defer stop()

	// Start checks the minimum length, so shift the clock instead of waiting 5 seconds.
	base := time.Now()
	s.now = func() time.Time { return base }
	tm, err := s.Start(context.Background(), 42, 5*time.Second, "чай")
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.pending[tm.ID].Reset(10 * time.Millisecond)
	s.mu.Unlock()

	n.wait(t)
	if n.fired[0].Label != "чай" || n.fired[0].ChatID != 42 {
		t.Errorf("fired %+v", n.fired[0])
	}
	if left, _ := s.List(context.Background(), 42); len(left) != 0 {
		t.Errorf("fired timer still stored: %+v", left)
	}
}

func TestCancel(t *testing.T) {
	st := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	n := newFake()
	s := New(st, n)
	stop := run(t, s)
	defer stop()
	ctx := context.Background()

	tm, err := s.Start(ctx, 1, time.Minute, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(ctx, 2, tm.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("other chat cancelled the timer: %v", err)
	}
	if err := s.Cancel(ctx, 1, tm.ID); err != nil {
		t.Fatal(err)
	}
	if s.pendingCount() != 0 {
		t.Error("timer still scheduled after cancel")
	}
}

func TestLimitPerChat(t *testing.T) {
	st := openStore(t, filepath.Join(t.TempDir(), "t.db"))
	s := New(st, newFake())
	stop := run(t, s)
	defer stop()
	ctx := context.Background()

	for i := 0; i < MaxPerChat; i++ {
		if _, err := s.Start(ctx, 7, time.Hour, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Start(ctx, 7, time.Hour, ""); !errors.Is(err, ErrTooMany) {
		t.Errorf("limit not enforced: %v", err)
	}
	if _, err := s.Start(ctx, 8, time.Hour, ""); err != nil {
		t.Errorf("other chat affected by limit: %v", err)
	}
}

func TestRestoreAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	ctx := context.Background()

	// Bot was down: one timer is overdue, another is still in the future.
	st := openStore(t, path)
	if _, err := st.AddTimer(ctx, store.Timer{ChatID: 1, Label: "overdue", FireAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTimer(ctx, store.Timer{ChatID: 1, Label: "future", FireAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	n := newFake()
	s := New(st, n)
	stop := run(t, s)
	n.wait(t)
	if n.fired[0].Label != "overdue" || n.late[0] < 50*time.Second {
		t.Errorf("overdue timer: %+v late %v", n.fired[0], n.late[0])
	}
	if s.pendingCount() != 1 {
		t.Errorf("future timer not scheduled, pending %d", s.pendingCount())
	}
	stop()

	// Shutdown must keep the future timer in the database for the next start.
	left, _ := st.ListTimers(ctx, 0)
	if len(left) != 1 || left[0].Label != "future" {
		t.Errorf("after shutdown: %+v", left)
	}
}
