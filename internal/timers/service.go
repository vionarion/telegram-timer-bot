// Package timers runs countdowns: it stores them, schedules them in memory and notifies when they fire.
package timers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/vionarion/telegram-timer-bot/internal/duration"
	"github.com/vionarion/telegram-timer-bot/internal/store"
)

// MaxPerChat limits active timers in one chat so a single user can't flood the bot.
const MaxPerChat = 20

var ErrTooMany = fmt.Errorf("в чате уже %d таймеров, это максимум", MaxPerChat)

// Notifier delivers the "time is up" message; the Telegram bot implements it.
type Notifier interface {
	TimerFired(ctx context.Context, t store.Timer, late time.Duration)
}

var (
	activeGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "timerbot_active_timers", Help: "Timers waiting to fire.",
	})
	firedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "timerbot_timers_fired_total", Help: "Timers that fired.",
	})
	startedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "timerbot_timers_started_total", Help: "Timers started.",
	})
)

func init() {
	prometheus.MustRegister(activeGauge, firedTotal, startedTotal)
}

type Service struct {
	store  *store.Store
	notify Notifier
	now    func() time.Time

	mu      sync.Mutex
	pending map[int64]*time.Timer
	wg      sync.WaitGroup
	ctx     context.Context
}

func New(st *store.Store, n Notifier) *Service {
	return &Service{store: st, notify: n, now: time.Now, pending: map[int64]*time.Timer{}}
}

// Run restores timers saved before a restart and keeps the service alive until ctx is done.
// Timers that should have fired while the bot was down fire right away with a "late" note.
func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()

	saved, err := s.store.ListTimers(ctx, 0)
	if err != nil {
		return fmt.Errorf("restore timers: %w", err)
	}
	for _, t := range saved {
		s.schedule(t)
	}
	slog.Info("timers restored", "count", len(saved))

	<-ctx.Done()
	s.mu.Lock()
	for id, tm := range s.pending {
		if tm.Stop() {
			s.wg.Done() // the callback will never run
		}
		delete(s.pending, id)
	}
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

func (s *Service) Start(ctx context.Context, chatID int64, d time.Duration, label string) (store.Timer, error) {
	if d < duration.Min || d > duration.Max {
		return store.Timer{}, errors.New("длительность вне допустимых границ")
	}
	n, err := s.store.CountTimers(ctx, chatID)
	if err != nil {
		return store.Timer{}, err
	}
	if n >= MaxPerChat {
		return store.Timer{}, ErrTooMany
	}
	t := store.Timer{ChatID: chatID, Label: label, FireAt: s.now().Add(d).Truncate(time.Second)}
	if t.ID, err = s.store.AddTimer(ctx, t); err != nil {
		return store.Timer{}, err
	}
	startedTotal.Inc()
	s.schedule(t)
	return t, nil
}

func (s *Service) Cancel(ctx context.Context, chatID, id int64) error {
	if err := s.store.DeleteTimer(ctx, chatID, id); err != nil {
		return err
	}
	s.mu.Lock()
	if tm, ok := s.pending[id]; ok {
		if tm.Stop() {
			s.wg.Done()
		}
		delete(s.pending, id)
		activeGauge.Dec()
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) List(ctx context.Context, chatID int64) ([]store.Timer, error) {
	return s.store.ListTimers(ctx, chatID)
}

func (s *Service) schedule(t store.Timer) {
	wait := t.FireAt.Sub(s.now())
	if wait < 0 {
		wait = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wg.Add(1)
	s.pending[t.ID] = time.AfterFunc(wait, func() {
		defer s.wg.Done()
		s.fire(t)
	})
	activeGauge.Inc()
}

func (s *Service) fire(t store.Timer) {
	s.mu.Lock()
	_, still := s.pending[t.ID]
	delete(s.pending, t.ID)
	ctx := s.ctx
	s.mu.Unlock()
	if !still || ctx == nil {
		return // cancelled in the meantime or service stopped
	}
	activeGauge.Dec()

	// Delete first: if the delete fails we must not notify twice after a restart.
	if err := s.store.DeleteTimer(ctx, t.ChatID, t.ID); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("delete fired timer", "id", t.ID, "err", err)
		}
		return
	}
	firedTotal.Inc()
	late := s.now().Sub(t.FireAt)
	if late < 5*time.Second {
		late = 0
	}
	s.notify.TimerFired(ctx, t, late)
}

// pendingCount is used by tests.
func (s *Service) pendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}
