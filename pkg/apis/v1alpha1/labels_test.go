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

package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestInstanceTypeDerivedLabelKeys(t *testing.T) {
	require.Equal(t, "node.kubernetes.io/instance-family", LabelInstanceFamily)
	require.Equal(t, "node.kubernetes.io/instance-size", LabelInstanceSize)
	require.Equal(t, "karpenter.alibabacloud.com/instance-family", LabelInstanceFamilyCanonical)
	require.Equal(t, "karpenter.alibabacloud.com/instance-category", LabelInstanceCategory)
	require.Equal(t, "karpenter.alibabacloud.com/instance-generation", LabelInstanceGeneration)
	require.Equal(t, "karpenter.alibabacloud.com/instance-size", LabelInstanceSizeCanonical)

	for _, key := range []string{
		LabelInstanceFamily,
		LabelInstanceFamilyCanonical,
		LabelInstanceCategory,
		LabelInstanceGeneration,
		LabelInstanceSize,
		LabelInstanceSizeCanonical,
	} {
		require.Contains(t, WellKnownLabels(), key)
		require.True(t, karpv1.WellKnownLabels.Has(key))
	}
}
