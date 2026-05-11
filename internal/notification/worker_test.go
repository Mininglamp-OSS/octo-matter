// Copyright 2026 MININGLAMP Technology and the OCTO contributors
// SPDX-License-Identifier: Apache-2.0

package notification

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestWorker_SubmitAndShutdown(t *testing.T) {
	w := NewWorker(10, 2)
	var count atomic.Int32

	for i := 0; i < 5; i++ {
		w.Submit(func() {
			count.Add(1)
		})
	}

	w.Shutdown()
	if got := count.Load(); got != 5 {
		t.Errorf("expected 5 tasks executed, got %d", got)
	}
}

func TestWorker_DropWhenFull(t *testing.T) {
	// Buffer=2, 1 slow worker — excess tasks should be dropped.
	w := NewWorker(2, 1)
	var count atomic.Int32

	// Block the worker so buffer fills up.
	blocker := make(chan struct{})
	w.Submit(func() {
		<-blocker
		count.Add(1)
	})
	// Give the worker time to pick up the blocker.
	time.Sleep(10 * time.Millisecond)

	// These 2 fill the buffer.
	w.Submit(func() { count.Add(1) })
	w.Submit(func() { count.Add(1) })

	// This one should be dropped (buffer full, worker blocked).
	w.Submit(func() { count.Add(1) })

	// Unblock and shutdown.
	close(blocker)
	w.Shutdown()

	got := count.Load()
	if got > 3 {
		t.Errorf("expected at most 3 tasks executed (1 dropped), got %d", got)
	}
}

func TestWorker_RecoversPanic(t *testing.T) {
	w := NewWorker(10, 2)
	done := make(chan struct{})

	w.Submit(func() { panic("test") })
	w.Submit(func() { close(done) })

	select {
	case <-done:
		// Worker survived the panic and processed subsequent task.
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not recover from panic")
	}
	w.Shutdown()
}
