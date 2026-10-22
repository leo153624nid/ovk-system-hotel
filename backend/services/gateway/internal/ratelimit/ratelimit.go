// Package ratelimit ограничивает частоту запросов по ключу (обычно IP клиента). Состояние хранится в памяти процесса.
package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type visitor struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

type Limiter struct {
	rps   rate.Limit
	burst int
	idle  time.Duration
	now   func() time.Time

	mu       sync.Mutex
	visitors map[string]*visitor
}

// New создаёт лимитер: каждому ключу доступен burst запросов сразу и rps запросов в секунду дальше.
// Записи ключей, неактивных дольше idle, удаляются методом Run.
func New(rps rate.Limit, burst int, idle time.Duration) *Limiter {
	return &Limiter{
		rps:      rps,
		burst:    burst,
		idle:     idle,
		now:      time.Now,
		visitors: make(map[string]*visitor),
	}
}

// Allow сообщает, можно ли выполнить запрос сейчас. Если нельзя, возвращает, через сколько стоит повторить.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.now()

	l.mu.Lock()
	v, ok := l.visitors[key]
	if !ok {
		v = &visitor{lim: rate.NewLimiter(l.rps, l.burst)}
		l.visitors[key] = v
	}
	v.lastSeen = now
	l.mu.Unlock()

	res := v.lim.ReserveN(now, 1)
	if !res.OK() {
		return false, time.Second
	}

	if delay := res.DelayFrom(now); delay > 0 {
		res.CancelAt(now)
		return false, delay
	}

	return true, 0
}

// Run периодически чистит неактивные ключи до отмены ctx.
func (l *Limiter) Run(ctx context.Context) {
	t := time.NewTicker(l.idle)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.sweep()
		}
	}
}

func (l *Limiter) sweep() {
	cutoff := l.now().Add(-l.idle)

	l.mu.Lock()
	defer l.mu.Unlock()

	for k, v := range l.visitors {
		if v.lastSeen.Before(cutoff) {
			delete(l.visitors, k)
		}
	}
}

func (l *Limiter) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.visitors)
}
