package hue

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadPacerAdjustment(t *testing.T) {
	p := newReadPacer()
	for range 100 {
		p.success(0)
	}
	if p.rate != 10 {
		t.Fatal(p.rate)
	}
	p.throttle(time.Second)
	if p.rate != 5 || p.successes != 0 {
		t.Fatal(p.rate, p.successes)
	}
	for range 100 {
		p.success(0)
	}
	if p.rate != 5 {
		t.Fatal("old in-flight successes undid backoff")
	}
	for range 10 {
		p.success(p.epoch)
	}
	if p.rate != 6 {
		t.Fatal(p.rate)
	}
	for range 10 {
		p.throttle(0)
	}
	if p.rate != 1 {
		t.Fatal(p.rate)
	}
}
func TestReadPacerQueuedBackoff(t *testing.T) {
	p := newReadPacer()
	if _, err := p.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan time.Time, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		if _, err := p.acquire(ctx); err == nil {
			done <- time.Now()
		}
	}()
	p.throttle(500 * time.Millisecond)
	until := p.pausedUntil
	select {
	case got := <-done:
		if got.Before(until) {
			t.Fatal("queued GET ignored shared cooldown")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := p.acquire(ctx2); err == nil {
		t.Fatal("cancel ignored")
	}
}
func TestClientFourConcurrentReads(t *testing.T) {
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var active, peak atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		w.Write([]byte(`{"errors":[],"data":[]}`))
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Raw(context.Background(), "/clip/v2/resource/light"); err != nil {
				t.Error(err)
			}
		}()
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			close(release)
			wg.Wait()
			t.Fatal("GETs remained serial")
		}
	}
	select {
	case <-entered:
		close(release)
		wg.Wait()
		t.Fatal("more than four active GETs")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	if peak.Load() != 4 {
		t.Fatal(peak.Load())
	}
}
func TestClientWritesRemainSerial(t *testing.T) {
	var active, peak atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		if n > peak.Load() {
			peak.Store(n)
		}
		time.Sleep(220 * time.Millisecond)
		w.Write([]byte(`{"errors":[],"data":[]}`))
	})
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.request(context.Background(), http.MethodPut, "/clip/v2/resource/scene/test", map[string]any{}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak.Load() != 1 {
		t.Fatal("writes ran concurrently")
	}
}
