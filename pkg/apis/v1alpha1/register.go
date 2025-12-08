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
	"github.com/awslabs/operatorpkg/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "karpenter.alibabacloud.com", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme

	// ECSNodeClassConditions defines the condition types for ECSNodeClass
	ECSNodeClassConditions = status.NewReadyConditions(
		ConditionTypeVSwitchResolved,
		ConditionTypeSecurityGroupResolved,
		ConditionTypeImageResolved,
		ConditionTypeRAMRoleResolved,
	)
)

func init() {
	metav1.AddToGroupVersion(scheme.Scheme, GroupVersion)
	scheme.Scheme.AddKnownTypes(GroupVersion,
		&ECSNodeClass{},
		&ECSNodeClassList{},
	)
}

// addKnownTypes adds the set of types defined in this package to the supplied scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&ECSNodeClass{},
		&ECSNodeClassList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

// GetConditions returns the status conditions of the ECSNodeClass
func (in *ECSNodeClass) GetConditions() []status.Condition {
	// Convert metav1.Condition to status.Condition
	conditions := make([]status.Condition, len(in.Status.Conditions))
	for i, c := range in.Status.Conditions {
		conditions[i] = status.Condition(c)
	}
	return conditions
}

// SetConditions sets the status conditions of the ECSNodeClass
func (in *ECSNodeClass) SetConditions(conditions []status.Condition) {
	// Convert status.Condition to metav1.Condition
	metav1Conditions := make([]metav1.Condition, len(conditions))
	for i, c := range conditions {
		metav1Conditions[i] = metav1.Condition(c)
	}
	in.Status.Conditions = metav1Conditions
}

// StatusConditions returns the condition set for the ECSNodeClass
func (in *ECSNodeClass) StatusConditions() status.ConditionSet {
	return ECSNodeClassConditions.For(in)
}
