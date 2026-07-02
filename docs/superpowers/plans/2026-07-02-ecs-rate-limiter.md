# ECS Rate Limiter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a token bucket rate limiter at the `DefaultECSClient` level and reduce `withThrottlingRetry` to 2 attempts, so API traffic is proactively smoothed rather than reactively retried.

**Architecture:** A `rate.Limiter` (golang.org/x/time/rate) is embedded in `DefaultECSClient` and called before every ECS API method. This prevents thundering-herd scenarios at the source. `withThrottlingRetry` is reduced to 2 attempts (max ~2s blocking) as a safety net for bursts that slip through.

**Tech Stack:** `golang.org/x/time/rate` (already in go.mod), existing `ECSClient` interface in `pkg/clients/ecs.go`, existing retry in `pkg/providers/instance/retry.go`.

## Global Constraints

- Do not change the `ECSClient` interface — only the `DefaultECSClient` struct and its constructor.
- `golang.org/x/time v0.13.0` is the pinned version; do not upgrade.
- All existing tests must continue to pass; tests that use a mock `ECSClient` are unaffected (mock doesn't embed the limiter).
- Rate limits: 10 QPS, burst 20 — conservative defaults matching the most restrictive ECS API (RunInstances).
- `withThrottlingRetry` MaxAttempts reduced from 5 → 2 (max in-goroutine blocking ~2s: 1s initial + ~1s second attempt).

---

## File Map

| File | Change |
|------|--------|
| `pkg/clients/ecs.go` | Add `limiter *rate.Limiter` to `DefaultECSClient`; add `WithRateLimit` option; call `limiter.Wait(ctx)` in every API method |
| `pkg/errors/errors.go` | Change throttling `RetryStrategy.MaxAttempts` from 5 → 2 |
| `pkg/providers/instance/retry.go` | No change needed (reads MaxAttempts from `GetRetryStrategy`, picks up the change automatically) |
| `pkg/clients/ecs_test.go` | New: unit tests for rate limiter integration |

---

## Task 1: Add token bucket to `DefaultECSClient`

**Files:**
- Modify: `pkg/clients/ecs.go`
- Create: `pkg/clients/ecs_test.go`

**Interfaces:**
- Produces: `NewDefaultECSClient(client, region, opts...)` accepting `WithRateLimit(qps, burst)` option
- Produces: all existing `ECSClient` interface methods unchanged; internally throttled

- [ ] **Step 1: Write failing tests**

Create `pkg/clients/ecs_test.go`:

```go
package clients_test

import (
	"context"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
)

// fakeECSSDK counts calls and always returns a canned response.
type fakeECSSDK struct {
	runCalls int
}

func (f *fakeECSSDK) RunInstances(req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	f.runCalls++
	return &ecs.RunInstancesResponse{}, nil
}

// TestRateLimiter_SlowsHighFrequency verifies that N calls over a short window
// take longer than N/QPS seconds when the limiter is set to a low QPS.
func TestRateLimiter_SlowsHighFrequency(t *testing.T) {
	// 2 QPS, burst 1 → 3 calls should take at least 1s (2 waits of 0.5s each)
	// We can't call NewDefaultECSClient with a real SDK; instead we test the limiter
	// behaviour on the options struct directly.
	limiter := clients.NewLimiter(2, 1) // exported for testability

	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := limiter.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond {
		t.Errorf("expected >= 1s for 3 calls at 2 QPS, got %s", elapsed)
	}
}

// TestRateLimiter_CtxCancel verifies Wait returns ctx.Err() when context is cancelled.
func TestRateLimiter_CtxCancel(t *testing.T) {
	limiter := clients.NewLimiter(0.1, 1) // very slow: 1 token per 10s
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	limiter.Wait(ctx) // consume the burst token
	err := limiter.Wait(ctx) // should block then return context.DeadlineExceeded
	if err == nil {
		t.Fatal("expected context deadline error, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./pkg/clients/... -run TestRateLimiter -v
```

Expected: compilation failure — `clients.NewLimiter` not defined yet.

- [ ] **Step 3: Add rate limiter to `DefaultECSClient`**

Edit `pkg/clients/ecs.go`. Make the following changes:

**Add import** (insert `"golang.org/x/time/rate"` into the import block):

```go
import (
	"context"
	"fmt"
	"strings"
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"golang.org/x/time/rate"
)
```

**Replace the `DefaultECSClient` struct and constructor**:

```go
// DefaultECSClient implements ECSClient using Alibaba Cloud SDK.
// All API calls are gated by a shared token bucket to prevent thundering-herd
// throttling. Default: 10 QPS, burst 20.
type DefaultECSClient struct {
	client  *ecs.Client
	region  string
	limiter *rate.Limiter
}

// clientOption configures DefaultECSClient.
type clientOption func(*DefaultECSClient)

// WithRateLimit sets the token bucket rate limiter. qps is the sustained
// requests per second; burst is the maximum number of tokens that can
// accumulate. Use WithRateLimit(0, 0) to disable rate limiting.
func WithRateLimit(qps float64, burst int) clientOption {
	return func(c *DefaultECSClient) {
		if qps <= 0 || burst <= 0 {
			c.limiter = rate.NewLimiter(rate.Inf, 0)
		} else {
			c.limiter = rate.NewLimiter(rate.Limit(qps), burst)
		}
	}
}

// NewLimiter creates a rate.Limiter; exported only for white-box tests.
func NewLimiter(qps float64, burst int) *rate.Limiter {
	return rate.NewLimiter(rate.Limit(qps), burst)
}

// NewDefaultECSClient creates a new ECS client with a 10 QPS / burst-20
// token bucket by default. Pass WithRateLimit to override.
func NewDefaultECSClient(client *ecs.Client, region string, opts ...clientOption) ECSClient {
	c := &DefaultECSClient{
		client:  client,
		region:  region,
		limiter: rate.NewLimiter(rate.Limit(10), 20), // conservative default
	}
	for _, o := range opts {
		o(c)
	}
	return c
}
```

**Add `wait` helper at the top of the method block** (private, called inside every API method):

```go
// wait blocks until a token is available or ctx is cancelled.
func (c *DefaultECSClient) wait(ctx context.Context) error {
	return c.limiter.Wait(ctx)
}
```

**Update all API methods** to call `c.wait(ctx)` first and return on error. Update each of the 4 high-frequency methods:

```go
// RunInstances implements ECSClient interface
func (c *DefaultECSClient) RunInstances(ctx context.Context, request *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	return c.client.RunInstances(request)
}

// DescribeInstances implements ECSClient interface
func (c *DefaultECSClient) DescribeInstances(ctx context.Context, request *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	return c.client.DescribeInstances(request)
}

// DeleteInstances implements ECSClient interface
func (c *DefaultECSClient) DeleteInstances(ctx context.Context, request *ecs.DeleteInstancesRequest) (*ecs.DeleteInstancesResponse, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	return c.client.DeleteInstances(request)
}

// TagResources implements ECSClient interface
func (c *DefaultECSClient) TagResources(ctx context.Context, request *ecs.TagResourcesRequest) (*ecs.TagResourcesResponse, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	return c.client.TagResources(request)
}
```

Apply the same `c.wait(ctx)` pattern to the remaining lower-frequency methods: `CreateLaunchTemplate`, `DescribeLaunchTemplates`, `DeleteLaunchTemplate`, `DescribeInstanceTypes`, `DescribeZones`, `DescribeImages`, `DescribeSecurityGroups`, `DescribeCapacityReservations`, `DescribePrice`.

For `DescribeImages` (which has an internal pagination loop), call `c.wait(ctx)` once **per page** inside the loop — not once at the top — to respect the per-request limit:

```go
func (c *DefaultECSClient) DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	// ... request setup unchanged ...
	for {
		request.PageNumber = tea.Int32(int32(pageNumber))
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		response, err := c.client.DescribeImages(request)
		// ... rest unchanged ...
	}
}
```

- [ ] **Step 4: Build**

```bash
go build ./pkg/clients/...
```

Expected: no errors.

- [ ] **Step 5: Run tests**

```bash
go test ./pkg/clients/... -run TestRateLimiter -v -count=1
```

Expected:
```
=== RUN   TestRateLimiter_SlowsHighFrequency
--- PASS: TestRateLimiter_SlowsHighFrequency (1.0xs)
=== RUN   TestRateLimiter_CtxCancel
--- PASS: TestRateLimiter_CtxCancel (0.0xs)
```

- [ ] **Step 6: Run full test suite**

```bash
go test ./... -count=1 2>&1 | grep -E "FAIL|ok|---"
```

Expected: all packages `ok`, no `FAIL`.

- [ ] **Step 7: Commit**

```bash
git add pkg/clients/ecs.go pkg/clients/ecs_test.go
git commit -m "feat: add token bucket rate limiter to DefaultECSClient (10 QPS / burst 20)"
```

---

## Task 2: Reduce withThrottlingRetry to 2 attempts

**Files:**
- Modify: `pkg/errors/errors.go` (change `MaxAttempts` in the throttling strategy)

**Interfaces:**
- `withThrottlingRetry` in `retry.go` calls `pkgerrors.GetRetryStrategy(pkgerrors.ErrThrottling)` — it picks up the new value automatically, no change needed there.

- [ ] **Step 1: Write the failing test**

Add to `pkg/errors/errors_test.go` (create if not exists):

```go
package errors_test

import (
	"testing"

	pkgerrors "github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/errors"
)

func TestThrottlingStrategy_MaxAttempts(t *testing.T) {
	s := pkgerrors.GetRetryStrategy(pkgerrors.ErrThrottling)
	if s == nil {
		t.Fatal("expected non-nil strategy for ErrThrottling")
	}
	if s.MaxAttempts != 2 {
		t.Errorf("expected MaxAttempts=2, got %d", s.MaxAttempts)
	}
	if s.MaxBackoff > 2000 {
		t.Errorf("expected MaxBackoff<=2000ms (2s), got %d", s.MaxBackoff)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./pkg/errors/... -run TestThrottlingStrategy -v
```

Expected: `FAIL — expected MaxAttempts=2, got 5`

- [ ] **Step 3: Update the throttling RetryStrategy**

In `pkg/errors/errors.go`, find `GetRetryStrategy` and change the throttling branch:

```go
if IsThrottlingError(err) {
    return &RetryStrategy{
        MaxAttempts:       2,     // was 5; token bucket prevents most throttling
        InitialBackoff:    1000,  // 1s
        MaxBackoff:        2000,  // 2s max (was 16s); goroutine blocks at most ~2s
        BackoffMultiplier: 2.0,
    }
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./pkg/errors/... -run TestThrottlingStrategy -v
```

Expected: `PASS`

- [ ] **Step 5: Run full retry integration test**

```bash
go test ./pkg/providers/instance/... -count=1 -v 2>&1 | grep -E "PASS|FAIL|RUN"
```

Expected: all existing instance tests pass.

- [ ] **Step 6: Commit**

```bash
git add pkg/errors/errors.go pkg/errors/errors_test.go
git commit -m "fix: reduce throttling retry MaxAttempts 5→2, MaxBackoff 16s→2s"
```

---

## Task 3: End-to-end build and push

- [ ] **Step 1: Full build and vet**

```bash
go build ./... && go vet ./...
```

Expected: no output (success).

- [ ] **Step 2: Full test suite**

```bash
go test ./pkg/... -count=1 2>&1 | tail -20
```

Expected: all `ok`, no `FAIL`.

- [ ] **Step 3: Push**

```bash
git push origin fix/issue-10-throttling-backoff
```

---

## Verification Checklist

| What to verify | How |
|---------------|-----|
| Rate limiter slows burst of calls | `TestRateLimiter_SlowsHighFrequency`: 3 calls at 2 QPS takes ≥ 1s |
| Ctx cancel propagates through Wait | `TestRateLimiter_CtxCancel`: deadline error returned immediately |
| withThrottlingRetry uses MaxAttempts=2 | `TestThrottlingStrategy_MaxAttempts` |
| Existing instance Create/List/Delete tests pass | `go test ./pkg/providers/instance/...` |
| No interface change (mock ECSClient still works) | `go build ./...` with no errors |
