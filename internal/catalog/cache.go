package catalog

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type Loader interface {
	Load(context.Context) ([]Artist, error)
}

type Snapshot struct {
	Artists   []Artist
	FetchedAt time.Time
	Stale     bool
}

type CacheOptions struct {
	FreshFor    time.Duration
	MaxAge      time.Duration
	RetryAfter  time.Duration
	LoadTimeout time.Duration
}

func DefaultCacheOptions() CacheOptions {
	return CacheOptions{FreshFor: 5 * time.Minute, MaxAge: 24 * time.Hour, RetryAfter: 30 * time.Second, LoadTimeout: 10 * time.Second}
}

// Cache owns immutable snapshots. Callers must not modify returned artist data.
// Concurrent misses share one load. Stale reads return immediately and refresh
// in the background; failures cannot overwrite the last successful snapshot.
type Cache struct {
	mu        sync.Mutex
	loader    Loader
	options   CacheOptions
	ctx       context.Context
	now       func() time.Time
	data      Snapshot
	inflight  chan struct{}
	retryAt   time.Time
	lastError error
}

func NewCache(ctx context.Context, loader Loader, options CacheOptions) (*Cache, error) {
	if loader == nil || options.FreshFor <= 0 || options.MaxAge < options.FreshFor || options.RetryAfter <= 0 || options.LoadTimeout <= 0 {
		return nil, fmt.Errorf("invalid cache configuration")
	}
	return &Cache{ctx: ctx, loader: loader, options: options, now: time.Now}, nil
}

func (c *Cache) Get(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	c.mu.Lock()
	now := c.now()
	age := now.Sub(c.data.FetchedAt)
	hasData := len(c.data.Artists) > 0
	if hasData && age < c.options.FreshFor {
		data := c.data
		c.mu.Unlock()
		return data, nil
	}
	if c.inflight == nil && !now.Before(c.retryAt) {
		c.inflight = make(chan struct{})
		go c.refresh(c.inflight)
	}
	if hasData && age <= c.options.MaxAge {
		data := c.data
		data.Stale = true
		c.mu.Unlock()
		return data, nil
	}
	wait := c.inflight
	err := c.lastError
	c.mu.Unlock()
	if wait == nil {
		if err == nil {
			err = fmt.Errorf("catalog unavailable")
		}
		return Snapshot{}, err
	}
	select {
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-wait:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.lastError != nil {
			return Snapshot{}, c.lastError
		}
		return c.data, nil
	}
}

func (c *Cache) refresh(done chan struct{}) {
	ctx, cancel := context.WithTimeout(c.ctx, c.options.LoadTimeout)
	defer cancel()
	var artists []Artist
	var err error
	// Recovery at the goroutine boundary prevents a faulty loader from crashing
	// the process or leaving every waiting request blocked forever.
	func() {
		defer func() {
			if problem := recover(); problem != nil {
				err = fmt.Errorf("catalog loader panic")
				slog.Error("catalog loader panic", "reason", problem)
			}
		}()
		artists, err = c.loader.Load(ctx)
	}()
	if err == nil && len(artists) == 0 {
		err = fmt.Errorf("empty catalog")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastError = err
	if err == nil {
		c.data = Snapshot{Artists: artists, FetchedAt: c.now()}
		c.retryAt = time.Time{}
	} else {
		c.retryAt = c.now().Add(c.options.RetryAfter)
		slog.Warn("catalog refresh failed; previous snapshot retained", "error", err)
	}
	c.inflight = nil
	close(done)
}
