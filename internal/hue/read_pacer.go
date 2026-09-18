package hue

import (
	"context"
	"sync"
	"time"
)

// readPacer spaces GET starts without reserving future tokens. Every waiter
// rechecks shared backoff, including requests queued before a 429 response.
type readPacer struct {
	mu                sync.Mutex
	rate              float64
	successes         int
	epoch             uint64
	next, pausedUntil time.Time
}

func newReadPacer() *readPacer { return &readPacer{rate: 5} }
func (p *readPacer) acquire(ctx context.Context) (uint64, error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		p.mu.Lock()
		now := time.Now()
		ready := p.next
		if p.pausedUntil.After(ready) {
			ready = p.pausedUntil
		}
		if !now.Before(ready) {
			p.next = now.Add(time.Duration(float64(time.Second) / p.rate))
			epoch := p.epoch
			p.mu.Unlock()
			return epoch, nil
		}
		p.mu.Unlock()
		if err := wait(ctx, time.Until(ready)); err != nil {
			return 0, err
		}
	}
}
func (p *readPacer) success(epoch uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if epoch != p.epoch {
		return
	}
	p.successes++
	if p.successes >= 10 {
		p.successes = 0
		p.rate = min(10, p.rate+1)
	}
}
func (p *readPacer) throttle(delay time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rate = max(1, p.rate/2)
	p.successes = 0
	p.epoch++
	// Retry-After: 0 must still result in a shared reduction in traffic.
	until := time.Now().Add(max(delay, time.Duration(float64(time.Second)/p.rate)))
	if until.After(p.pausedUntil) {
		p.pausedUntil = until
	}
}
