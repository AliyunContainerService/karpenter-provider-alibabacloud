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
	"testing"
)

func TestIsInsufficientCapacityError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"no stock", fmt.Errorf("SDKError: %s", ErrCodeNoStock), true},
		{"zone not on sale", fmt.Errorf("SDKError: %s", ErrCodeZoneNotOnSale), true},
		{"vswitch ip not enough", fmt.Errorf("SDKError: %s", ErrCodeVSwitchIPNotEnough), true},
		{
			// Real message observed from RunInstances when an arm64 family is not
			// sellable in a given zone; must be treated as retryable so the launch
			// falls back to another zone's vSwitch.
			name: "resource type not supported in zone",
			err:  fmt.Errorf("user order resource type [EcsAmountChecker (instanceType: ecs.r8y.xlarge)] not exists in [cn-hangzhou-i]: %s", ErrCodeResourceTypeNotSupported),
			want: true,
		},
		{"quota exceeded not capacity", fmt.Errorf("SDKError: %s", ErrCodeQuotaExceedInstance), false},
		{"invalid instance type not capacity", fmt.Errorf("SDKError: %s", ErrCodeInvalidInstanceType), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsInsufficientCapacityError(tt.err); got != tt.want {
				t.Errorf("IsInsufficientCapacityError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
