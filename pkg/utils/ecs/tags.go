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

import "sort"

// MaxTagsPerRequest is the maximum number of tags allowed in a single ECS tag
// API request (RunInstances Tag parameter or TagResources Tag parameter).
// Alibaba Cloud ECS enforces this as a hard per-request limit, independent of
// the per-instance tag quota. See GitHub issue #14.
const MaxTagsPerRequest = 20

// BatchTags splits a tag map into multiple sub-maps, each containing at most
// MaxTagsPerRequest entries. This is required because the ECS API rejects
// requests with more than 20 tags (NumberExceed.Tags), even when the instance-
// level tag quota has been increased beyond 20.
//
// The keys are sorted before splitting to ensure deterministic batch boundaries,
// which makes the function's output stable across calls (important for testing
// and logging).
//
// Returns nil if the input map is empty.
func BatchTags(tags map[string]string) []map[string]string {
	if len(tags) == 0 {
		return nil
	}

	// Sort keys for deterministic batching
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var batches []map[string]string
	for i := 0; i < len(keys); i += MaxTagsPerRequest {
		end := i + MaxTagsPerRequest
		if end > len(keys) {
			end = len(keys)
		}
		batch := make(map[string]string, end-i)
		for _, k := range keys[i:end] {
			batch[k] = tags[k]
		}
		batches = append(batches, batch)
	}
	return batches
}
