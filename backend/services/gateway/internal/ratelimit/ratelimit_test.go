package ratelimit

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func newTest(burst int) (*Limiter, *time.Time) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	l := New(rate.Limit(1), burst, time.Minute)
	l.now = func() time.Time { return now }

	return l, &now
}

func TestBurstThenDeny(t *testing.T) {
	l, _ := newTest(3)
	for i := range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d within burst must pass", i+1)
		}
	}

	ok, retry := l.Allow("a")

	if ok {
		t.Fatal("request over burst must be denied")
	}
	if retry <= 0 || retry > time.Second {
		t.Fatalf("retry-after = %v, want within (0, 1s]", retry)
	}
}

func TestDeniedRequestsDoNotConsumeTokens(t *testing.T) {
	l, now := newTest(1)
	l.Allow("a")
	for range 5 {
		l.Allow("a") // отказы не должны отодвигать момент восстановления
	}

	*now = now.Add(time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("token must be available after 1s regardless of denied attempts")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l, _ := newTest(1)
	l.Allow("a")

	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("another key must have its own bucket")
	}
}

func TestSweepRemovesIdleKeys(t *testing.T) {
	l, now := newTest(1)
	l.Allow("old")

	*now = now.Add(2 * time.Minute)

	l.Allow("fresh")
	l.sweep()

	if l.size() != 1 {
		t.Fatalf("size = %d, want only the fresh key", l.size())
	}
}
