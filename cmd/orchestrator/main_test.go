package main

import (
	"context"
	"testing"
	"time"
)

func TestCompensationStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { compensationJob(ctx, nil, nil); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("compensation ignored cancellation")
	}
}
