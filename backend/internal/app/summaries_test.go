package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
)

type summaryFixture struct {
	mu    sync.Mutex
	now   time.Time
	calls atomic.Int32
	gate  chan struct{} // when set, each load waits for one receive
	err   error
	ids   []string
}

func (f *summaryFixture) server(ttl time.Duration) *Server {
	s := &Server{SummaryTTL: ttl}
	s.Clock = func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
	s.loadSummaries = func(ctx context.Context) (map[string]domain.Entry, error) {
		f.calls.Add(1)
		f.mu.Lock()
		gate, err, ids := f.gate, f.err, append([]string(nil), f.ids...)
		f.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if err != nil {
			return nil, err
		}
		out := map[string]domain.Entry{}
		for _, id := range ids {
			out[id] = domain.Entry{}
		}
		return out, nil
	}
	return s
}

func (f *summaryFixture) set(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }

func one(t *testing.T, s *Server) string {
	t.Helper()
	m, err := s.summaries(context.Background())
	if err != nil {
		t.Fatalf("summaries: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("expected one entry, got %d", len(m))
	}
	for k := range m {
		return k
	}
	return ""
}

func waitIdle(t *testing.T, s *Server) {
	t.Helper()
	for i := 0; i < 200; i++ {
		s.summaryCache.mu.Lock()
		idle := s.summaryCache.loading == nil
		s.summaryCache.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("refresh did not finish")
}

func TestSummariesWithoutTTLReadEveryTime(t *testing.T) {
	f := &summaryFixture{now: time.Unix(1000, 0), ids: []string{"a"}}
	s := f.server(0)
	for i := 0; i < 3; i++ {
		one(t, s)
	}
	if got := f.calls.Load(); got != 3 {
		t.Fatalf("loads = %d, want 3", got)
	}
}

func TestSummariesReuseFreshCopyAndServeStaleWhileRefreshing(t *testing.T) {
	f := &summaryFixture{now: time.Unix(1000, 0), ids: []string{"old"}}
	s := f.server(30 * time.Second)
	if k := one(t, s); k != "old" {
		t.Fatalf("first = %q", k)
	}
	f.set(func() { f.now = f.now.Add(10 * time.Second); f.ids = []string{"new"} })
	if k := one(t, s); k != "old" || f.calls.Load() != 1 {
		t.Fatalf("fresh copy not reused: %q after %d loads", k, f.calls.Load())
	}
	gate := make(chan struct{})
	f.set(func() { f.now = f.now.Add(25 * time.Second); f.gate = gate })
	// Expired: the old copy is returned at once while one refresh runs.
	for i := 0; i < 3; i++ {
		if k := one(t, s); k != "old" {
			t.Fatalf("stale copy not served during refresh: %q", k)
		}
	}
	gate <- struct{}{}
	waitIdle(t, s)
	if got := f.calls.Load(); got != 2 {
		t.Fatalf("loads = %d, want one refresh", got)
	}
	f.set(func() { f.gate = nil })
	if k := one(t, s); k != "new" {
		t.Fatalf("refreshed copy = %q", k)
	}
}

func TestSummariesColdLoadOutlivesRequestDeadline(t *testing.T) {
	gate := make(chan struct{})
	f := &summaryFixture{now: time.Unix(1000, 0), ids: []string{"a"}, gate: gate}
	s := f.server(30 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.summaries(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cold request err = %v, want deadline", err)
	}
	gate <- struct{}{}
	waitIdle(t, s)
	if k := one(t, s); k != "a" || f.calls.Load() != 1 {
		t.Fatalf("background load not reused: %q after %d loads", k, f.calls.Load())
	}
}

func TestSummariesFailuresAndMaximumStaleness(t *testing.T) {
	f := &summaryFixture{now: time.Unix(1000, 0), err: errors.New("database down")}
	s := f.server(30 * time.Second)
	if _, err := s.summaries(context.Background()); err == nil {
		t.Fatal("cold failure returned no error")
	}
	f.set(func() { f.err = nil; f.ids = []string{"a"} })
	one(t, s)
	// A failing refresh keeps serving the last copy within the staleness bound.
	f.set(func() { f.now = f.now.Add(time.Minute); f.err = errors.New("database down") })
	if k := one(t, s); k != "a" {
		t.Fatalf("stale copy = %q", k)
	}
	waitIdle(t, s)
	// Beyond the bound the caller waits for a refresh and sees its failure.
	f.set(func() { f.now = f.now.Add(summaryMaxStale) })
	if _, err := s.summaries(context.Background()); err == nil {
		t.Fatal("copy older than the staleness bound was served")
	}
}

func TestWarmSummariesLoadsBeforeFirstRequest(t *testing.T) {
	f := &summaryFixture{now: time.Unix(1000, 0), ids: []string{"a"}}
	s := f.server(30 * time.Second)
	s.WarmSummaries()
	waitIdle(t, s)
	if k := one(t, s); k != "a" || f.calls.Load() != 1 {
		t.Fatalf("warm load not reused: %q after %d loads", k, f.calls.Load())
	}
	f.set(func() {})
	s = f.server(0)
	s.WarmSummaries()
	if f.calls.Load() != 1 {
		t.Fatal("warming without a TTL loaded summaries")
	}
}
