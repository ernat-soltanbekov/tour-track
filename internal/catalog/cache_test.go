package catalog

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type loadFunc func(context.Context) ([]Artist, error)

func (f loadFunc) Load(ctx context.Context) ([]Artist, error) { return f(ctx) }

func testCache(t *testing.T, loader Loader) (*Cache, *atomic.Int64) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, err := NewCache(ctx, loader, CacheOptions{FreshFor: time.Minute, MaxAge: time.Hour, RetryAfter: time.Second, LoadTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	clock := &atomic.Int64{}
	clock.Store(time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC).UnixNano())
	c.now = func() time.Time { return time.Unix(0, clock.Load()) }
	return c, clock
}

func waitRefresh(t *testing.T, c *Cache) {
	t.Helper()
	c.mu.Lock()
	done := c.inflight
	c.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("refresh did not finish")
		}
	}
}

func TestCacheCoalescesConcurrentMisses(t *testing.T) {
	var calls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	c, _ := testCache(t, loadFunc(func(ctx context.Context) ([]Artist, error) {
		calls.Add(1)
		close(started)
		<-release
		return []Artist{{ID: 1, Name: "Queen"}}, nil
	}))
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, e := c.Get(context.Background())
			if e == nil && len(s.Artists) != 1 {
				e = errors.New("missing data")
			}
			errs <- e
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("%d loads instead of 1", calls.Load())
	}
}

func TestStaleFallbackBackoffExpiryAndRecovery(t *testing.T) {
	var fail atomic.Bool
	var calls atomic.Int64
	c, clock := testCache(t, loadFunc(func(ctx context.Context) ([]Artist, error) {
		calls.Add(1)
		if fail.Load() {
			return nil, errors.New("offline")
		}
		return []Artist{{ID: int(calls.Load()), Name: "saved"}}, nil
	}))
	first, err := c.Get(context.Background())
	if err != nil || first.Stale {
		t.Fatal("first load", err)
	}
	fail.Store(true)
	clock.Add(int64(2 * time.Minute))
	stale, err := c.Get(context.Background())
	if err != nil || !stale.Stale || stale.Artists[0].ID != 1 {
		t.Fatal("stale data lost", err)
	}
	waitRefresh(t, c)
	for range 20 {
		if _, err := c.Get(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("backoff ignored: %d calls", calls.Load())
	}
	clock.Add(int64(2 * time.Hour))
	if _, err := c.Get(context.Background()); err == nil {
		t.Fatal("expired stale data served")
	}
	fail.Store(false)
	clock.Add(int64(2 * time.Second))
	recovered, err := c.Get(context.Background())
	if err != nil || recovered.Stale || recovered.Artists[0].ID <= 1 {
		t.Fatal("did not recover", err)
	}
}

func TestRequestCancellationDoesNotCancelSharedLoad(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	c, _ := testCache(t, loadFunc(func(ctx context.Context) ([]Artist, error) {
		close(started)
		select {
		case <-release:
			return []Artist{{ID: 1}}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := c.Get(ctx); result <- err }()
	<-started
	cancel()
	if !errors.Is(<-result, context.Canceled) {
		t.Fatal("caller not canceled")
	}
	close(release)
	if _, err := c.Get(context.Background()); err != nil {
		t.Fatal("shared load canceled", err)
	}
	if _, err := c.Get(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled request ignored")
	}
}

func TestCacheHandlesPanicEmptyAndTimeout(t *testing.T) {
	for name, loader := range map[string]loadFunc{
		"panic":   func(context.Context) ([]Artist, error) { panic("test failure") },
		"empty":   func(context.Context) ([]Artist, error) { return nil, nil },
		"timeout": func(ctx context.Context) ([]Artist, error) { <-ctx.Done(); return nil, ctx.Err() },
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := testCache(t, loader)
			c.options.LoadTimeout = 10 * time.Millisecond
			if _, err := c.Get(context.Background()); err == nil {
				t.Fatal("expected error")
			}
			if _, err := c.Get(context.Background()); err == nil {
				t.Fatal("backoff must preserve error")
			}
		})
	}
}

func TestCacheRejectsBadConfig(t *testing.T) {
	if _, err := NewCache(context.Background(), nil, DefaultCacheOptions()); err == nil {
		t.Fatal("nil loader")
	}
	if _, err := NewCache(context.Background(), loadFunc(func(context.Context) ([]Artist, error) { return nil, nil }), CacheOptions{}); err == nil {
		t.Fatal("zero durations")
	}
}
