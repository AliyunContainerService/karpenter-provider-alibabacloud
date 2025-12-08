/*
Copyright 2024 The Alibaba Cloud Karpenter Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package batcher

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// BatchWindow implements time-based batching window for aggregating requests
// This is aligned with AWS Provider's batching strategy:
// - Idle Duration: 1ms (wait for more requests if none arrive) - 更快的响应速度
// - Max Duration: 10ms (maximum wait time before force flush) - 更快的批处理
type BatchWindow struct {
	key             string
	items           []*BatchItem
	idleDuration    time.Duration
	maxDuration     time.Duration
	idleTimer       *time.Timer
	maxTimer        *time.Timer
	mu              sync.Mutex
	executor        BatchExecutor
	started         bool
	closed          bool
	flushInProgress bool
}

// BatchItem represents a single request in the batch
type BatchItem struct {
	Request  interface{}
	ResultCh chan *BatchResult
	AddedAt  time.Time
}

// BatchExecutor executes a batch of items
type BatchExecutor interface {
	Execute(ctx context.Context, items []*BatchItem) error
}

// NewBatchWindow creates a new batch window
func NewBatchWindow(
	key string,
	idleDuration time.Duration,
	maxDuration time.Duration,
	executor BatchExecutor,
) *BatchWindow {
	return &BatchWindow{
		key:          key,
		items:        make([]*BatchItem, 0),
		idleDuration: idleDuration,
		maxDuration:  maxDuration,
		executor:     executor,
	}
}

// Add adds a request to the batch window
// The window will automatically flush when:
// 1. No new requests arrive within idleDuration (e.g., 10ms)
// 2. maxDuration is reached (e.g., 100ms)
func (w *BatchWindow) Add(item *BatchItem) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return fmt.Errorf("batch window closed")
	}

	item.AddedAt = time.Now()
	w.items = append(w.items, item)

	if !w.started {
		// First request, start timers
		w.idleTimer = time.AfterFunc(w.idleDuration, w.flushIdle)
		w.maxTimer = time.AfterFunc(w.maxDuration, w.flushMax)
		w.started = true
	} else {
		// Reset idle timer (we got a new request)
		if w.idleTimer != nil {
			w.idleTimer.Reset(w.idleDuration)
		}
	}

	return nil
}

// flushIdle is called when idle timeout is reached
func (w *BatchWindow) flushIdle() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.flushInProgress {
		w.flush("idle_timeout")
	}
}

// flushMax is called when max timeout is reached
func (w *BatchWindow) flushMax() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.flushInProgress {
		w.flush("max_timeout")
	}
}

// flush executes the batch (must be called with lock held)
func (w *BatchWindow) flush(reason string) {
	if len(w.items) == 0 || w.flushInProgress {
		return
	}

	w.flushInProgress = true

	// Stop timers
	if w.idleTimer != nil {
		w.idleTimer.Stop()
		w.idleTimer = nil
	}
	if w.maxTimer != nil {
		w.maxTimer.Stop()
		w.maxTimer = nil
	}

	// Collect items to process
	items := w.items
	w.items = make([]*BatchItem, 0)
	w.started = false

	// Record metrics
	batchSize := len(items)
	startTime := time.Now()

	// Execute batch asynchronously (release lock)
	go func() {
		defer func() {
			w.mu.Lock()
			w.flushInProgress = false
			w.mu.Unlock()
		}()

		ctx := context.Background()
		err := w.executor.Execute(ctx, items)

		// Record batch metrics
		duration := time.Since(startTime)
		recordBatchMetrics(w.key, batchSize, reason, duration, err)

		// If execution failed but didn't distribute results, do it here
		if err != nil {
			for _, item := range items {
				select {
				case item.ResultCh <- &BatchResult{Error: err}:
				default:
					// Channel might be closed
				}
			}
		}
	}()
}

// Close closes the batch window and flushes any pending items
func (w *BatchWindow) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return
	}

	w.closed = true
	if !w.flushInProgress {
		w.flush("close")
	}
}

// Size returns the current number of items in the window
func (w *BatchWindow) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.items)
}

// WindowedBatcher manages multiple batch windows keyed by request type
type WindowedBatcher struct {
	windows         map[string]*BatchWindow
	mu              sync.RWMutex
	idleDuration    time.Duration
	maxDuration     time.Duration
	executorFactory func(key string) BatchExecutor
	cleanupInterval time.Duration
	windowTTL       time.Duration
	lastAccess      map[string]time.Time
	stopCleanup     chan struct{}
	cleanupOnce     sync.Once
}

// NewWindowedBatcher creates a new windowed batcher
func NewWindowedBatcher(
	idleDuration time.Duration,
	maxDuration time.Duration,
	executorFactory func(key string) BatchExecutor,
) *WindowedBatcher {
	wb := &WindowedBatcher{
		windows:         make(map[string]*BatchWindow),
		idleDuration:    idleDuration,
		maxDuration:     maxDuration,
		executorFactory: executorFactory,
		cleanupInterval: 5 * time.Minute,
		windowTTL:       10 * time.Minute,
		lastAccess:      make(map[string]time.Time),
		stopCleanup:     make(chan struct{}),
	}

	// Start cleanup goroutine
	wb.cleanupOnce.Do(func() {
		go wb.cleanupLoop()
	})

	return wb
}

// Batch adds a request to the appropriate batch window
func (wb *WindowedBatcher) Batch(
	key string,
	request interface{},
	resultCh chan *BatchResult,
) error {
	wb.mu.Lock()

	// Update last access time
	wb.lastAccess[key] = time.Now()

	// Get or create window
	window, ok := wb.windows[key]
	if !ok {
		executor := wb.executorFactory(key)
		window = NewBatchWindow(key, wb.idleDuration, wb.maxDuration, executor)
		wb.windows[key] = window
	}
	wb.mu.Unlock()

	// Add to window
	item := &BatchItem{
		Request:  request,
		ResultCh: resultCh,
	}

	return window.Add(item)
}

// Close closes the batcher and releases resources
func (wb *WindowedBatcher) Close() error {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	// Stop cleanup goroutine
	close(wb.stopCleanup)

	// Close all windows
	for _, window := range wb.windows {
		window.Close()
	}

	// Clear windows map
	wb.windows = make(map[string]*BatchWindow)
	wb.lastAccess = make(map[string]time.Time)

	return nil
}

// Stats returns batcher statistics
func (wb *WindowedBatcher) Stats() map[string]interface{} {
	wb.mu.RLock()
	defer wb.mu.RUnlock()

	stats := make(map[string]interface{})
	stats["active_windows"] = len(wb.windows)

	// Count pending items
	pendingItems := 0
	for _, window := range wb.windows {
		pendingItems += window.Size()
	}
	stats["pending_items"] = pendingItems

	return stats
}

// cleanupLoop periodically removes expired windows
func (wb *WindowedBatcher) cleanupLoop() {
	ticker := time.NewTicker(wb.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			wb.cleanupExpiredWindows()
		case <-wb.stopCleanup:
			return
		}
	}
}

// cleanupExpiredWindows removes windows that haven't been accessed for windowTTL
func (wb *WindowedBatcher) cleanupExpiredWindows() {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	now := time.Now()
	expirationTime := now.Add(-wb.windowTTL)

	for key, lastAccess := range wb.lastAccess {
		if lastAccess.Before(expirationTime) {
			// Close and remove expired window
			if window, ok := wb.windows[key]; ok {
				window.Close()
				delete(wb.windows, key)
			}
			delete(wb.lastAccess, key)
		}
	}
}
