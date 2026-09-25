package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAcquireSerializesAndCancels(t *testing.T) {
	r := New(nil)
	r.Update("media", makeResp(10, 1, time.Now().Add(time.Minute)))
	release, err := r.Acquire(context.Background(), "media")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status("media").Remaining != 0 {
		t.Fatal("not reserved")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Acquire(ctx, "media"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	other, err := r.Acquire(context.Background(), "lookup")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	if _, err := r.Acquire(ctx, "media"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestAcquireAfterResetAndMissingHeaders(t *testing.T) {
	r := New(nil)
	r.On429("media", time.Now().Add(-time.Second))
	release, err := r.Acquire(context.Background(), "media")
	if err != nil {
		t.Fatal(err)
	}
	release()
	r.Update("media", makeResp(10, 1, time.Now().Add(time.Minute)))
	release, err = r.Acquire(context.Background(), "media")
	if err != nil {
		t.Fatal(err)
	}
	r.Update("media", nil)
	release()
	if r.Status("media").Remaining != 0 {
		t.Fatal("invented budget")
	}
}

func TestConcurrentAcquireWaitsForHeaders(t *testing.T) {
	r := New(nil)
	release, err := r.Acquire(context.Background(), "media")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		next, err := r.Acquire(ctx, "media")
		if err == nil {
			next()
		}
		done <- err
	}()
	select {
	case <-done:
		t.Fatal("second request overtook first")
	case <-time.After(20 * time.Millisecond):
	}
	// The first response exhausts the budget before releasing its reservation.
	r.Update("media", makeResp(10, 0, time.Now().Add(time.Minute)))
	release()
	select {
	case <-done:
		t.Fatal("ignored exhausted budget")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait not cancellable")
	}
}
