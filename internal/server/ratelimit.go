package server

import (
	"sync"

	"golang.org/x/time/rate"
)

// limiters holds a token-bucket rate limiter per ingest token, keyed by token
// name. A runaway authenticated producer is throttled rather than flooding a
// Matrix room (E2EE sends are expensive). Token count is small and bounded,
// so no eviction is needed.
type limiters struct {
	mu    sync.Mutex
	m     map[string]*rate.Limiter
	rate  rate.Limit
	burst int
}

// newLimiters returns a limiter set, or nil (disabled) when perSecond <= 0.
func newLimiters(perSecond float64, burst int) *limiters {
	if perSecond <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = 1
	}
	return &limiters{
		m:     make(map[string]*rate.Limiter),
		rate:  rate.Limit(perSecond),
		burst: burst,
	}
}

// allow reports whether a request for the given token key may proceed. A nil
// limiters set always allows.
func (l *limiters) allow(key string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	lim, ok := l.m[key]
	if !ok {
		lim = rate.NewLimiter(l.rate, l.burst)
		l.m[key] = lim
	}
	l.mu.Unlock()
	return lim.Allow()
}

// NewLimiters is the exported constructor for wiring from main.
func NewLimiters(perSecond float64, burst int) *limiters {
	return newLimiters(perSecond, burst)
}
