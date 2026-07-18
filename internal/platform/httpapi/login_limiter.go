package httpapi

import (
	"math"
	"sync"
	"time"
)

const (
	defaultLoginLimiterMaxKeys = 10_000
	overflowLoginLimiterKey    = "__overflow__"
)

type loginLimiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	maxKeys   int
	nextSweep time.Time
	attempts  map[string][]time.Time
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{
		limit: limit, window: window, maxKeys: defaultLoginLimiterMaxKeys,
		attempts: make(map[string][]time.Time),
	}
}

func (l *loginLimiter) Allow(key string, now time.Time) (bool, int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	l.prune(cutoff, now)
	key = l.bucketKey(key)
	attempts := l.attempts[key]
	kept := attempts[:0]
	for _, attemptedAt := range attempts {
		if attemptedAt.After(cutoff) {
			kept = append(kept, attemptedAt)
		}
	}
	if len(kept) >= l.limit {
		retryAfter := int(math.Ceil(kept[0].Add(l.window).Sub(now).Seconds()))
		if retryAfter < 1 {
			retryAfter = 1
		}
		l.attempts[key] = kept
		return false, 0, retryAfter
	}
	kept = append(kept, now)
	l.attempts[key] = kept
	return true, l.limit - len(kept), 0
}

func (l *loginLimiter) prune(cutoff, now time.Time) {
	if !l.nextSweep.IsZero() && now.Before(l.nextSweep) {
		return
	}
	for key, attempts := range l.attempts {
		kept := attempts[:0]
		for _, attemptedAt := range attempts {
			if attemptedAt.After(cutoff) {
				kept = append(kept, attemptedAt)
			}
		}
		if len(kept) == 0 {
			delete(l.attempts, key)
		} else {
			l.attempts[key] = kept
		}
	}
	l.nextSweep = now.Add(l.window)
}

func (l *loginLimiter) bucketKey(key string) string {
	if _, exists := l.attempts[key]; exists || key == overflowLoginLimiterKey {
		return key
	}
	if l.maxKeys <= 1 || len(l.attempts) >= l.maxKeys-1 {
		return overflowLoginLimiterKey
	}
	return key
}

func (l *loginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
