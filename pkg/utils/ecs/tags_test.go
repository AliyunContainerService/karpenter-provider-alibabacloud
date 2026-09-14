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

package ecs

import (
	"fmt"
	"testing"
)

func TestBatchTags(t *testing.T) {
	tests := []struct {
		name          string
		input         map[string]string
		expectedLen   int
		expectedSizes []int
		checkContent  bool
	}{
		{
			name:          "empty map",
			input:         map[string]string{},
			expectedLen:   0,
			expectedSizes: nil,
		},
		{
			name:          "nil map",
			input:         nil,
			expectedLen:   0,
			expectedSizes: nil,
		},
		{
			name: "single tag",
			input: map[string]string{
				"key1": "value1",
			},
			expectedLen:   1,
			expectedSizes: []int{1},
			checkContent:  true,
		},
		{
			name: "exactly 20 tags",
			input: func() map[string]string {
				m := make(map[string]string)
				for i := 0; i < 20; i++ {
					m[fmt.Sprintf("key%02d", i)] = fmt.Sprintf("value%02d", i)
				}
				return m
			}(),
			expectedLen:   1,
			expectedSizes: []int{20},
			checkContent:  true,
		},
		{
			name: "21 tags - splits into 2 batches",
			input: func() map[string]string {
				m := make(map[string]string)
				for i := 0; i < 21; i++ {
					m[fmt.Sprintf("key%02d", i)] = fmt.Sprintf("value%02d", i)
				}
				return m
			}(),
			expectedLen:   2,
			expectedSizes: []int{20, 1},
			checkContent:  true,
		},
		{
			name: "50 tags - splits into 3 batches",
			input: func() map[string]string {
				m := make(map[string]string)
				for i := 0; i < 50; i++ {
					m[fmt.Sprintf("key%02d", i)] = fmt.Sprintf("value%02d", i)
				}
				return m
			}(),
			expectedLen:   3,
			expectedSizes: []int{20, 20, 10},
			checkContent:  true,
		},
		{
			name: "100 tags - splits into 5 batches",
			input: func() map[string]string {
				m := make(map[string]string)
				for i := 0; i < 100; i++ {
					m[fmt.Sprintf("key%03d", i)] = fmt.Sprintf("value%03d", i)
				}
				return m
			}(),
			expectedLen:   5,
			expectedSizes: []int{20, 20, 20, 20, 20},
			checkContent:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BatchTags(tt.input)

			// Check number of batches
			if len(result) != tt.expectedLen {
				t.Errorf("expected %d batches, got %d", tt.expectedLen, len(result))
			}

			// Check sizes of each batch
			if tt.expectedSizes != nil {
				for i, expectedSize := range tt.expectedSizes {
					if i >= len(result) {
						t.Errorf("missing batch %d", i)
						continue
					}
					if len(result[i]) != expectedSize {
						t.Errorf("batch %d: expected size %d, got %d", i, expectedSize, len(result[i]))
					}
				}
			}

			// Check that no batch exceeds MaxTagsPerRequest
			for i, batch := range result {
				if len(batch) > MaxTagsPerRequest {
					t.Errorf("batch %d exceeds MaxTagsPerRequest: %d > %d", i, len(batch), MaxTagsPerRequest)
				}
			}

			// Check content preservation
			if tt.checkContent && tt.input != nil {
				// Reconstruct the original map from batches
				reconstructed := make(map[string]string)
				for _, batch := range result {
					for k, v := range batch {
						reconstructed[k] = v
					}
				}

				// Verify all original tags are present
				if len(reconstructed) != len(tt.input) {
					t.Errorf("reconstructed map has %d tags, original has %d", len(reconstructed), len(tt.input))
				}

				for k, v := range tt.input {
					if reconstructed[k] != v {
						t.Errorf("tag %s: expected value %q, got %q", k, v, reconstructed[k])
					}
				}
			}
		})
	}
}

func TestBatchTags_Deterministic(t *testing.T) {
	// Test that BatchTags produces deterministic output across multiple calls
	input := make(map[string]string)
	for i := 0; i < 30; i++ {
		input[fmt.Sprintf("key%02d", i)] = fmt.Sprintf("value%02d", i)
	}

	result1 := BatchTags(input)
	result2 := BatchTags(input)

	if len(result1) != len(result2) {
		t.Fatalf("non-deterministic: first call produced %d batches, second produced %d", len(result1), len(result2))
	}

	for i := range result1 {
		if len(result1[i]) != len(result2[i]) {
			t.Errorf("batch %d: first call has %d tags, second has %d", i, len(result1[i]), len(result2[i]))
		}

		for k, v := range result1[i] {
			if result2[i][k] != v {
				t.Errorf("batch %d, key %s: first call has value %q, second has %q", i, k, v, result2[i][k])
			}
		}
	}
}

func TestBatchTags_NoDuplicateKeys(t *testing.T) {
	// Test that no key appears in multiple batches
	input := make(map[string]string)
	for i := 0; i < 45; i++ {
		input[fmt.Sprintf("key%02d", i)] = fmt.Sprintf("value%02d", i)
	}

	batches := BatchTags(input)
	seen := make(map[string]bool)

	for batchIdx, batch := range batches {
		for k := range batch {
			if seen[k] {
				t.Errorf("key %q appears in multiple batches (found again in batch %d)", k, batchIdx)
			}
			seen[k] = true
		}
	}

	// Verify all keys from input are present
	if len(seen) != len(input) {
		t.Errorf("expected %d unique keys, found %d", len(input), len(seen))
	}
}
