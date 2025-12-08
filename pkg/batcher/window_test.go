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
	"sync/atomic"
	"testing"
	"time"
)

// TestBatchWindow tests basic batch window functionality
func TestBatchWindow(t *testing.T) {
	executor := &mockExecutor{
		execFunc: func(ctx context.Context, items []*BatchItem) error {
			// Distribute results
			for i, item := range items {
				item.ResultCh <- &BatchResult{
					Value: fmt.Sprintf("result-%d", i),
					Error: nil,
				}
			}
			return nil
		},
	}

	window := NewBatchWindow("test", 10*time.Millisecond, 100*time.Millisecond, executor)
	defer window.Close()

	// Add 3 items
	resultCh1 := make(chan *BatchResult, 1)
	resultCh2 := make(chan *BatchResult, 1)
	resultCh3 := make(chan *BatchResult, 1)

	err := window.Add(&BatchItem{Request: "req1", ResultCh: resultCh1})
	if err != nil {
		t.Fatalf("Failed to add item 1: %v", err)
	}

	err = window.Add(&BatchItem{Request: "req2", ResultCh: resultCh2})
	if err != nil {
		t.Fatalf("Failed to add item 2: %v", err)
	}

	err = window.Add(&BatchItem{Request: "req3", ResultCh: resultCh3})
	if err != nil {
		t.Fatalf("Failed to add item 3: %v", err)
	}

	// Wait for results (should batch within 10ms)
	timeout := time.After(200 * time.Millisecond)

	results := make([]string, 0)
	for i := 0; i < 3; i++ {
		select {
		case result := <-resultCh1:
			if result.Error != nil {
				t.Fatalf("Result 1 error: %v", result.Error)
			}
			results = append(results, result.Value.(string))
		case result := <-resultCh2:
			if result.Error != nil {
				t.Fatalf("Result 2 error: %v", result.Error)
			}
			results = append(results, result.Value.(string))
		case result := <-resultCh3:
			if result.Error != nil {
				t.Fatalf("Result 3 error: %v", result.Error)
			}
			results = append(results, result.Value.(string))
		case <-timeout:
			t.Fatalf("Timeout waiting for results, got %d/3", len(results))
		}
	}

	// Verify executor was called only once
	if executor.execCount != 1 {
		t.Errorf("Expected executor to be called once, got %d", executor.execCount)
	}
}

// TestBatchWindowIdleFlush tests idle timeout flushing
func TestBatchWindowIdleFlush(t *testing.T) {
	var batchSize int32

	executor := &mockExecutor{
		execFunc: func(ctx context.Context, items []*BatchItem) error {
			atomic.StoreInt32(&batchSize, int32(len(items)))
			for _, item := range items {
				item.ResultCh <- &BatchResult{Value: "ok"}
			}
			return nil
		},
	}

	window := NewBatchWindow("test", 20*time.Millisecond, 200*time.Millisecond, executor)
	defer window.Close()

	// Add items with delay less than idle timeout
	resultCh1 := make(chan *BatchResult, 1)
	resultCh2 := make(chan *BatchResult, 1)

	window.Add(&BatchItem{Request: "req1", ResultCh: resultCh1})
	time.Sleep(5 * time.Millisecond)
	window.Add(&BatchItem{Request: "req2", ResultCh: resultCh2})

	// Wait for idle flush
	time.Sleep(50 * time.Millisecond)

	size := atomic.LoadInt32(&batchSize)
	if size != 2 {
		t.Errorf("Expected batch size 2, got %d", size)
	}
}

// TestBatchWindowMaxFlush tests max duration flushing
func TestBatchWindowMaxFlush(t *testing.T) {
	var batchSize int32

	executor := &mockExecutor{
		execFunc: func(ctx context.Context, items []*BatchItem) error {
			atomic.StoreInt32(&batchSize, int32(len(items)))
			for _, item := range items {
				item.ResultCh <- &BatchResult{Value: "ok"}
			}
			return nil
		},
	}

	window := NewBatchWindow("test", 50*time.Millisecond, 100*time.Millisecond, executor)
	defer window.Close()

	// Add items continuously to prevent idle flush
	for i := 0; i < 5; i++ {
		resultCh := make(chan *BatchResult, 1)
		window.Add(&BatchItem{Request: fmt.Sprintf("req%d", i), ResultCh: resultCh})
		time.Sleep(15 * time.Millisecond) // Less than idle timeout
	}

	// Wait for max flush
	time.Sleep(150 * time.Millisecond)

	size := atomic.LoadInt32(&batchSize)
	if size < 3 {
		t.Errorf("Expected batch size >= 3, got %d", size)
	}
}

// TestWindowedBatcher tests the windowed batcher
func TestWindowedBatcher(t *testing.T) {
	executorFactory := func(key string) BatchExecutor {
		return &mockExecutor{
			execFunc: func(ctx context.Context, items []*BatchItem) error {
				for i, item := range items {
					item.ResultCh <- &BatchResult{
						Value: fmt.Sprintf("%s-result-%d", key, i),
					}
				}
				return nil
			},
		}
	}

	wb := NewWindowedBatcher(10*time.Millisecond, 100*time.Millisecond, executorFactory)
	defer wb.Close()

	// Add requests to different windows
	resultCh1 := make(chan *BatchResult, 1)
	resultCh2 := make(chan *BatchResult, 1)
	resultCh3 := make(chan *BatchResult, 1)

	err := wb.Batch("key1", "req1", resultCh1)
	if err != nil {
		t.Fatalf("Failed to batch req1: %v", err)
	}

	err = wb.Batch("key1", "req2", resultCh2)
	if err != nil {
		t.Fatalf("Failed to batch req2: %v", err)
	}

	err = wb.Batch("key2", "req3", resultCh3)
	if err != nil {
		t.Fatalf("Failed to batch req3: %v", err)
	}

	// Wait for results
	timeout := time.After(200 * time.Millisecond)

	for i := 0; i < 3; i++ {
		select {
		case result := <-resultCh1:
			if result.Error != nil {
				t.Errorf("Result 1 error: %v", result.Error)
			}
		case result := <-resultCh2:
			if result.Error != nil {
				t.Errorf("Result 2 error: %v", result.Error)
			}
		case result := <-resultCh3:
			if result.Error != nil {
				t.Errorf("Result 3 error: %v", result.Error)
			}
		case <-timeout:
			t.Fatal("Timeout waiting for results")
		}
	}

	// Check stats
	stats := wb.Stats()
	if stats["active_windows"].(int) == 0 {
		t.Log("Windows were cleaned up (expected)")
	}
}

// TestBatcherTTLCache tests TTL-based caching
func TestBatcherTTLCache(t *testing.T) {
	b := NewBatcher(100*time.Millisecond, 10*time.Millisecond)
	defer b.Close()

	callCount := 0
	fn := func() (interface{}, error) {
		callCount++
		return "result", nil
	}

	// First call - cache miss
	result, err := b.DedupWithTTL(context.Background(), "test-key", 100*time.Millisecond, fn)
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}
	if result != "result" {
		t.Errorf("Expected 'result', got %v", result)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}

	// Second call - cache hit
	result, err = b.DedupWithTTL(context.Background(), "test-key", 100*time.Millisecond, fn)
	if err != nil {
		t.Fatalf("Second call failed: %v", err)
	}
	if result != "result" {
		t.Errorf("Expected 'result', got %v", result)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call (cache hit), got %d", callCount)
	}

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Third call - cache expired
	result, err = b.DedupWithTTL(context.Background(), "test-key", 100*time.Millisecond, fn)
	if err != nil {
		t.Fatalf("Third call failed: %v", err)
	}
	if callCount != 2 {
		t.Errorf("Expected 2 calls (cache expired), got %d", callCount)
	}
}

// TestConcurrentBatching tests concurrent requests to the same window
func TestConcurrentBatching(t *testing.T) {
	var execCount int32

	executor := &mockExecutor{
		execFunc: func(ctx context.Context, items []*BatchItem) error {
			atomic.AddInt32(&execCount, 1)
			for i, item := range items {
				item.ResultCh <- &BatchResult{
					Value: fmt.Sprintf("result-%d", i),
				}
			}
			return nil
		},
	}

	window := NewBatchWindow("test", 20*time.Millisecond, 100*time.Millisecond, executor)
	defer window.Close()

	// Launch 100 concurrent requests
	const numRequests = 100
	var wg sync.WaitGroup
	wg.Add(numRequests)

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			defer wg.Done()

			resultCh := make(chan *BatchResult, 1)
			err := window.Add(&BatchItem{
				Request:  fmt.Sprintf("req-%d", id),
				ResultCh: resultCh,
			})
			if err != nil {
				t.Errorf("Failed to add request %d: %v", id, err)
				return
			}

			// Wait for result
			select {
			case result := <-resultCh:
				if result.Error != nil {
					t.Errorf("Request %d failed: %v", id, result.Error)
				}
			case <-time.After(2 * time.Second):
				t.Errorf("Request %d timeout", id)
			}
		}(i)
	}

	wg.Wait()

	// Verify all requests were batched (not 100 individual executions)
	count := atomic.LoadInt32(&execCount)
	if count >= numRequests {
		t.Errorf("Expected batching (< %d executions), got %d", numRequests, count)
	}
	t.Logf("Successfully batched %d requests into %d executions", numRequests, count)
}

// mockExecutor is a mock BatchExecutor for testing
type mockExecutor struct {
	execFunc  func(ctx context.Context, items []*BatchItem) error
	execCount int32
}

func (m *mockExecutor) Execute(ctx context.Context, items []*BatchItem) error {
	atomic.AddInt32(&m.execCount, 1)
	if m.execFunc != nil {
		return m.execFunc(ctx, items)
	}
	return nil
}
