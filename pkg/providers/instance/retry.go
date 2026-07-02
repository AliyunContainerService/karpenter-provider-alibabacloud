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

package instance

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"time"

	pkgerrors "github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// withThrottlingRetry executes fn with exponential backoff on throttling errors.
// Non-throttling errors are returned immediately without retry.
// Backoff sleeps respect context cancellation.
func withThrottlingRetry[T any](ctx context.Context, operation string, fn func() (T, error)) (T, error) {
	logger := log.FromContext(ctx)
	strategy := pkgerrors.GetRetryStrategy(pkgerrors.ErrThrottling)

	var zero T
	var lastErr error

	for attempt := 0; attempt < strategy.MaxAttempts; attempt++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err

		if !pkgerrors.IsThrottlingError(err) {
			return zero, err
		}

		backoff := computeBackoff(strategy, attempt)
		logger.Info("throttled, retrying", "operation", operation,
			"attempt", attempt+1, "backoff", backoff)

		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(backoff):
		}
	}
	return zero, fmt.Errorf("%s throttled, failed after %d attempts: %w",
		operation, strategy.MaxAttempts, lastErr)
}

// computeBackoff calculates the exponential backoff duration with ±25% jitter.
func computeBackoff(s *pkgerrors.RetryStrategy, attempt int) time.Duration {
	ms := float64(s.InitialBackoff) * math.Pow(s.BackoffMultiplier, float64(attempt))
	if ms > float64(s.MaxBackoff) {
		ms = float64(s.MaxBackoff)
	}
	jitter := (rand.Float64() - 0.5) * ms * 0.5
	ms += jitter
	if ms < float64(s.InitialBackoff) {
		ms = float64(s.InitialBackoff)
	}
	return time.Duration(ms) * time.Millisecond
}
