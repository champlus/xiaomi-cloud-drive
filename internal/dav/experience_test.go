package dav

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/champlus/xiaomi-cloud-drive/internal/cloud"
)

type experienceBackend struct {
	*fake
	mu               sync.Mutex
	calls            int
	started, release chan struct{}
	fail             bool
}

func (b *experienceBackend) List(ctx context.Context, id string) ([]cloud.Entry, error) {
	b.mu.Lock()
	b.calls++
	n := b.calls
	b.mu.Unlock()
	if n == 1 {
		close(b.started)
		select {
		case <-b.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if b.fail {
		return nil, errors.New("temporary network failure")
	}
	name := "fresh"
	if n == 1 {
		name = "old"
	}
	return []cloud.Entry{{ID: name, Name: name, Type: "file"}}, nil
}
func TestInvalidatedDirectoryDoesNotReuseStaleInFlightResult(t *testing.T) {
	b := &experienceBackend{fake: newFake(), started: make(chan struct{}), release: make(chan struct{})}
	f := New(b)
	done := make(chan struct{})
	go func() { defer close(done); f.List(context.Background(), "0") }()
	<-b.started
	defer func() { close(b.release); <-done }()
	f.Invalidate()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	items, err := f.List(ctx, "0")
	if err != nil || len(items) != 1 || items[0].Name != "fresh" {
		t.Fatal("new directory request waited for or reused an invalidated listing")
	}
}
func TestFailedBackgroundRefreshDoesNotHammerCloud(t *testing.T) {
	b := &experienceBackend{fake: newFake(), started: make(chan struct{}), release: make(chan struct{}), fail: true}
	f := New(b)
	f.cache["0"] = cached{[]cloud.Entry{{ID: "saved", Name: "saved"}}, time.Now().Add(-35 * time.Second)}
	f.List(context.Background(), "0")
	<-b.started
	f.mu.Lock()
	flight := f.flight["0"]
	f.mu.Unlock()
	close(b.release)
	<-flight.done
	f.List(context.Background(), "0")
	f.mu.Lock()
	second := f.flight["0"]
	f.mu.Unlock()
	if second != nil {
		<-second.done
		t.Fatal("failed directory refresh was immediately retried by another browse")
	}
}

type indexLagBackend struct {
	*fake
	mu    sync.Mutex
	calls int
}

func (b *indexLagBackend) List(context.Context, string) ([]cloud.Entry, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	return nil, nil
}
func TestDirectoryIndexWaitUsesBoundedBackoff(t *testing.T) {
	b := &indexLagBackend{fake: newFake()}
	f := New(b)
	ctx, cancel := context.WithTimeout(context.Background(), 650*time.Millisecond)
	defer cancel()
	_, err := f.Await(ctx, "0", "pending", "sample", false)
	if err == nil {
		t.Fatal("expected delayed index to respect cancellation")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls > 3 {
		t.Fatalf("index wait made %d cloud reads in 650ms", b.calls)
	}
}
