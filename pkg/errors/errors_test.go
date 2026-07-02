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

package errors_test

import (
	"fmt"
	"testing"

	pkgerrors "github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/errors"
)

func TestErrThrottling_MatchesIsThrottlingError(t *testing.T) {
	if !pkgerrors.IsThrottlingError(pkgerrors.ErrThrottling) {
		t.Fatal("ErrThrottling sentinel must match IsThrottlingError")
	}
}

func TestGetRetryStrategy_ThrottlingMaxAttempts(t *testing.T) {
	s := pkgerrors.GetRetryStrategy(pkgerrors.ErrThrottling)
	if s == nil {
		t.Fatal("expected non-nil RetryStrategy for ErrThrottling")
	}
	if s.MaxAttempts != 2 {
		t.Errorf("expected MaxAttempts=2, got %d", s.MaxAttempts)
	}
	if s.MaxBackoff > 2000 {
		t.Errorf("expected MaxBackoff ≤ 2000ms, got %d", s.MaxBackoff)
	}
}

func TestGetRetryStrategy_NonThrottlingReturnsNonNil(t *testing.T) {
	// InternalError should still have a strategy
	s := pkgerrors.GetRetryStrategy(fmt.Errorf("InternalError occurred"))
	if s == nil {
		t.Fatal("expected non-nil RetryStrategy for InternalError")
	}
}

func TestGetRetryStrategy_ParameterErrorReturnsNil(t *testing.T) {
	// Non-retryable errors return nil
	s := pkgerrors.GetRetryStrategy(fmt.Errorf("InvalidParameter: bad field"))
	if s != nil {
		t.Errorf("expected nil RetryStrategy for parameter error, got %+v", s)
	}
}
