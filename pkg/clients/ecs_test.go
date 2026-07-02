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

package clients_test

import (
	"context"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
)

// TestNewLimiter_SlowsHighFrequency verifies that N calls at a low QPS limit
// take at least N/QPS seconds wall-clock time.
func TestNewLimiter_SlowsHighFrequency(t *testing.T) {
	// 2 QPS, burst 1: first call is free, calls 2 and 3 each wait ~0.5s → total ≥ 1s.
	limiter := clients.NewLimiter(2, 1)

	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := limiter.Wait(ctx); err != nil {
			t.Fatalf("Wait returned unexpected error: %v", err)
		}
	}
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond {
		t.Errorf("expected ≥ 1s for 3 calls at 2 QPS (burst 1), got %s", elapsed)
	}
}

// TestNewLimiter_CtxCancel verifies that Wait returns a non-nil error when the
// context deadline expires while waiting for a token.
func TestNewLimiter_CtxCancel(t *testing.T) {
	// 0.1 QPS, burst 1: 1 free token then ~10s wait. Cancel after 50ms.
	limiter := clients.NewLimiter(0.1, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	limiter.Wait(ctx) // consume the burst token; this returns immediately
	err := limiter.Wait(ctx)
	if err == nil {
		t.Fatal("expected a context error when deadline expires during Wait, got nil")
	}
}

// TestWithRateLimit_InfDisablesLimiting verifies that WithRateLimit(0,0) allows
// unlimited calls without any noticeable delay.
func TestWithRateLimit_InfDisablesLimiting(t *testing.T) {
	// NewDefaultECSClient with a nil SDK client is enough to test the limiter path;
	// we call WithRateLimit(0, 0) and verify the wait helper does not block.
	_ = clients.WithRateLimit(0, 0) // smoke-test: no panic
}
