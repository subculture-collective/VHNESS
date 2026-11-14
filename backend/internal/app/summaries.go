package app

import (
	"context"
	"sync"
	"time"

	"git.subcult.tv/subculture-collective/vhness/internal/domain"
)

// Building episode summaries reads every published episode's source inventory
// and snapshot metadata, which takes seconds for a large collection. Requests
// share a recent copy instead of repeating that work under their 3-second
// database deadline. Single-episode reads, playback and channel resolution
// still query the database on every request, so a revoked episode cannot play
// while it remains in a cached listing.
const (
	summaryLoadTimeout = 30 * time.Second
	// A listing may wait this long for a shared load in progress, instead of the
	// 3-second per-request database deadline; a cold load can take longer.
	summaryColdWait = 10 * time.Second
	// A refresh that keeps failing must not serve an old listing indefinitely.
	summaryMaxStale = 5 * time.Minute
)

type summaryCache struct {
	mu      sync.Mutex
	entries map[string]domain.Entry
	at      time.Time
	err     error
	loading chan struct{}
}

func (s *Server) now() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

// summaries returns episode summaries in this instance's collection scope.
// With SummaryTTL zero every call reads the database. Otherwise a copy younger
// than SummaryTTL is reused; an older copy (up to summaryMaxStale) is returned
// while one background refresh runs. Without a usable copy the caller waits for
// that refresh until its own context ends; the refresh keeps running under its
// own deadline, so the next request can use the result.
func (s *Server) summaries(ctx context.Context) (map[string]domain.Entry, error) {
	if s.SummaryTTL <= 0 {
		return s.summaryLoader()(ctx)
	}
	c := &s.summaryCache
	c.mu.Lock()
	age := s.now().Sub(c.at)
	if c.entries != nil && age < s.SummaryTTL {
		entries := c.entries
		c.mu.Unlock()
		return entries, nil
	}
	s.startSummaryRefresh()
	if c.entries != nil && age < summaryMaxStale {
		entries := c.entries
		c.mu.Unlock()
		return entries, nil
	}
	done := c.loading
	c.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	return c.entries, nil
}

// WarmSummaries starts loading listing summaries in the background, so the
// first listing requests after startup do not wait for a cold load.
func (s *Server) WarmSummaries() {
	if s.SummaryTTL <= 0 {
		return
	}
	s.summaryCache.mu.Lock()
	defer s.summaryCache.mu.Unlock()
	s.startSummaryRefresh()
}

func (s *Server) summaryLoader() func(context.Context) (map[string]domain.Entry, error) {
	if s.loadSummaries != nil {
		return s.loadSummaries
	}
	return func(ctx context.Context) (map[string]domain.Entry, error) {
		return s.DB.ScopedSummaries(ctx, s.Collections)
	}
}

// startSummaryRefresh starts one background load; the caller holds the cache lock.
func (s *Server) startSummaryRefresh() {
	c := &s.summaryCache
	if c.loading == nil {
		done := make(chan struct{})
		c.loading = done
		go s.refreshSummaries(s.summaryLoader(), done)
	}
}

func (s *Server) refreshSummaries(load func(context.Context) (map[string]domain.Entry, error), done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), summaryLoadTimeout)
	defer cancel()
	entries, err := load(ctx)
	c := &s.summaryCache
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
	if err == nil {
		c.entries, c.at = entries, s.now()
	}
	c.loading = nil
	close(done)
}
