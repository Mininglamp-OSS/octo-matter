package notification

import (
	"log"
	"sync"
)

// Worker provides a bounded goroutine pool for notification delivery.
// It replaces unbounded SafeGo calls with a fixed-size buffer and worker
// count, preventing goroutine explosion under burst load.
type Worker struct {
	ch   chan func()
	wg   sync.WaitGroup
	done chan struct{}
}

// NewWorker creates a worker pool with the given buffer size and worker count.
func NewWorker(bufSize, workers int) *Worker {
	w := &Worker{
		ch:   make(chan func(), bufSize),
		done: make(chan struct{}),
	}
	w.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go w.run()
	}
	return w
}

func (w *Worker) run() {
	defer w.wg.Done()
	for fn := range w.ch {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("ERROR: notification worker panic: %v", r)
				}
			}()
			fn()
		}()
	}
}

// Submit queues a function for async execution. If the buffer is full,
// the task is dropped and a warning is logged (non-blocking).
func (w *Worker) Submit(fn func()) {
	select {
	case w.ch <- fn:
	default:
		log.Printf("WARN: notification worker queue full, dropping task")
	}
}

// Shutdown closes the work channel and waits for all in-flight tasks to complete.
func (w *Worker) Shutdown() {
	close(w.ch)
	w.wg.Wait()
}
