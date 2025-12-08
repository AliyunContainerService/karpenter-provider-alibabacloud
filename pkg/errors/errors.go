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

package errors

import (
	"fmt"
	"strings"
)

// Error codes from Alibaba Cloud ECS API
const (
	// Resource shortage errors (Retryable with fallback)
	ErrCodeNoStock             = "OperationDenied.NoStock"
	ErrCodeZoneNotOnSale       = "Zone.NotOnSale"
	ErrCodeInsufficientBalance = "InsufficientBalance"
	ErrCodeVSwitchIPNotEnough  = "InvalidVSwitchId.IpNotEnough"

	// Quota errors (Not retryable)
	ErrCodeQuotaExceedInstance = "QuotaExceed.Instance"
	ErrCodeQuotaExceedSpot     = "QuotaExceed.Spot"
	ErrCodeQuotaExceedElastic  = "QuotaExceeded.ElasticQuota"

	// Throttling errors (Retryable with exponential backoff)
	ErrCodeThrottling         = "Throttling"
	ErrCodeThrottlingUser     = "Throttling.User"
	ErrCodeServiceUnavailable = "ServiceUnavailable"
	ErrCodeInternalError      = "InternalError"

	// Parameter errors (Not retryable)
	ErrCodeInvalidParameter       = "InvalidParameter"
	ErrCodeInvalidInstanceType    = "InvalidInstanceType.NotSupported"
	ErrCodeInvalidImageNotFound   = "InvalidImage.NotFound"
	ErrCodeInvalidVSwitchNotFound = "InvalidVSwitchId.NotFound"
	ErrCodeInvalidSGNotFound      = "InvalidSecurityGroupId.NotFound"

	// Permission errors (Not retryable)
	ErrCodeForbiddenRAM           = "Forbidden.RAM"
	ErrCodeForbiddenRiskControl   = "Forbidden.RiskControl"
	ErrCodeAccountStatusNotEnough = "InvalidAccountStatus.NotEnoughBalance"

	// Instance state errors
	ErrCodeIncorrectInstanceStatus = "IncorrectInstanceStatus"
	ErrCodeInstanceNotFound        = "InvalidInstanceId.NotFound"
	ErrCodeDeletionProtection      = "OperationDenied.DeletionProtection"
)

// IsInsufficientCapacityError checks if the error is due to insufficient capacity
func IsInsufficientCapacityError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, ErrCodeNoStock) ||
		strings.Contains(errMsg, ErrCodeZoneNotOnSale) ||
		strings.Contains(errMsg, ErrCodeVSwitchIPNotEnough)
}

// IsThrottlingError checks if the error is due to API throttling
func IsThrottlingError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, ErrCodeThrottling) ||
		strings.Contains(errMsg, ErrCodeThrottlingUser)
}

// IsQuotaError checks if the error is due to quota limits
func IsQuotaError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, ErrCodeQuotaExceedInstance) ||
		strings.Contains(errMsg, ErrCodeQuotaExceedSpot)
}

// IsParameterError checks if the error is due to invalid parameters
func IsParameterError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, ErrCodeInvalidParameter) ||
		strings.Contains(errMsg, ErrCodeInvalidInstanceType)
}

// IsPermissionError checks if the error is due to permission issues
func IsPermissionError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, ErrCodeForbiddenRAM) ||
		strings.Contains(errMsg, ErrCodeForbiddenRiskControl)
}

// IsRetryable checks if the error is retryable
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Retryable: insufficient capacity, throttling
	// Not retryable: quota, parameter, permission errors
	return IsInsufficientCapacityError(err) || IsThrottlingError(err)
}

// NewInsufficientCapacityError creates a new insufficient capacity error
func NewInsufficientCapacityError(instanceType, zone string) error {
	return fmt.Errorf("insufficient capacity for instance type %s in zone %s", instanceType, zone)
}

// NewNotFoundError creates a new not found error
func NewNotFoundError(resourceType, identifier string) error {
	return fmt.Errorf("%s not found: %s", resourceType, identifier)
}

// RetryStrategy defines retry behavior for different error types
type RetryStrategy struct {
	MaxAttempts       int
	InitialBackoff    int // milliseconds
	MaxBackoff        int // milliseconds
	BackoffMultiplier float64
}

// GetRetryStrategy returns the retry strategy for a given error
func GetRetryStrategy(err error) *RetryStrategy {
	if err == nil {
		return nil
	}

	if IsThrottlingError(err) {
		// Exponential backoff for throttling
		return &RetryStrategy{
			MaxAttempts:       5,
			InitialBackoff:    1000,  // 1s
			MaxBackoff:        16000, // 16s
			BackoffMultiplier: 2.0,
		}
	}

	if IsInsufficientCapacityError(err) {
		// Quick retry for capacity errors (should try different zone/type)
		return &RetryStrategy{
			MaxAttempts:       3,
			InitialBackoff:    500,  // 0.5s
			MaxBackoff:        2000, // 2s
			BackoffMultiplier: 1.5,
		}
	}

	// Internal errors
	if strings.Contains(err.Error(), ErrCodeInternalError) ||
		strings.Contains(err.Error(), ErrCodeServiceUnavailable) {
		return &RetryStrategy{
			MaxAttempts:       3,
			InitialBackoff:    2000, // 2s
			MaxBackoff:        8000, // 8s
			BackoffMultiplier: 2.0,
		}
	}

	// No retry for other errors
	return nil
}

// IsNotFound checks if error is a not found error
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	return strings.Contains(errMsg, ErrCodeInstanceNotFound) ||
		strings.Contains(errMsg, ErrCodeInvalidImageNotFound) ||
		strings.Contains(errMsg, ErrCodeInvalidVSwitchNotFound) ||
		strings.Contains(errMsg, ErrCodeInvalidSGNotFound) ||
		strings.Contains(errMsg, "not found") ||
		strings.Contains(errMsg, "NotFound") ||
		strings.Contains(errMsg, "InvalidInstanceId.NotFound") ||
		strings.Contains(errMsg, "InstanceNotFound")
}
