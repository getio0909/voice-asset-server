package httpapi

import (
	"testing"
	"time"
)

func TestLoginLimiterBoundsClientBucketsAndPrunesExpiredEntries(t *testing.T) {
	limiter := newLoginLimiter(2, time.Minute)
	limiter.maxKeys = 2
	now := time.Date(2026, 7, 16, 7, 0, 0, 0, time.UTC)

	if allowed, _, _ := limiter.Allow("client-1", now); !allowed {
		t.Fatal("first client was unexpectedly rejected")
	}
	if allowed, _, _ := limiter.Allow("client-2", now); !allowed {
		t.Fatal("overflow client was unexpectedly rejected")
	}
	if allowed, _, _ := limiter.Allow("client-3", now); !allowed {
		t.Fatal("second overflow attempt was unexpectedly rejected")
	}
	if allowed, _, _ := limiter.Allow("client-4", now); allowed {
		t.Fatal("shared overflow bucket did not enforce the limit")
	}
	if len(limiter.attempts) != limiter.maxKeys {
		t.Fatalf("bucket count = %d, want %d", len(limiter.attempts), limiter.maxKeys)
	}

	if allowed, _, _ := limiter.Allow("client-5", now.Add(2*time.Minute)); !allowed {
		t.Fatal("expired buckets were not pruned")
	}
	if len(limiter.attempts) != 1 {
		t.Fatalf("bucket count after prune = %d, want 1", len(limiter.attempts))
	}
}
