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

package status

import (
	"strings"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
)

func TestValidateResolvedSecurityGroupAttachLimitUsesResolvedSetSize(t *testing.T) {
	tests := []struct {
		name        string
		groups      []v1alpha1.SecurityGroup
		expectError string
	}{
		{
			name:        "rejects empty resolved set",
			groups:      []v1alpha1.SecurityGroup{},
			expectError: "resolved zero security groups",
		},
		{
			name: "accepts one resolved group",
			groups: []v1alpha1.SecurityGroup{
				{ID: "sg-1"},
			},
		},
		{
			name: "rejects resolved set over attach limit",
			groups: []v1alpha1.SecurityGroup{
				{ID: "sg-1"}, {ID: "sg-2"}, {ID: "sg-3"},
				{ID: "sg-4"}, {ID: "sg-5"}, {ID: "sg-6"},
			},
			expectError: "resolved 6 security groups",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResolvedSecurityGroupAttachLimit(tt.groups)
			if tt.expectError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.expectError) {
					t.Fatalf("expected error containing %q, got %v", tt.expectError, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
