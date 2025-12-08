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
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// SetupWebhookWithManager sets up the webhook with the Manager
func (nc *ECSNodeClass) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(nc).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-karpenter-alibabacloud-com-v1alpha1-ecsnodeclass,mutating=true,failurePolicy=fail,sideEffects=None,groups=karpenter.alibabacloud.com,resources=ecsnodeclasses,verbs=create;update,versions=v1alpha1,name=mecsnodeclass.kb.io,admissionReviewVersions=v1

var _ admission.CustomDefaulter = &ECSNodeClass{}

// Default implements admission.CustomDefaulter so a webhook will be registered for the type
func (nc *ECSNodeClass) Default(ctx context.Context, obj runtime.Object) error {
	// Set default system disk if not specified
	if nc.Spec.SystemDisk == nil {
		size := int32(40)
		perfLevel := "PL0"
		nc.Spec.SystemDisk = &SystemDiskSpec{
			Category:         "cloud_essd",
			Size:             &size,
			PerformanceLevel: &perfLevel,
		}
	} else {
		// Set default size if not specified
		if nc.Spec.SystemDisk.Size == nil {
			size := int32(40)
			nc.Spec.SystemDisk.Size = &size
		}
		// Set default category if empty
		if nc.Spec.SystemDisk.Category == "" {
			nc.Spec.SystemDisk.Category = "cloud_essd"
		}
		// Set default performance level for ESSD
		if nc.Spec.SystemDisk.Category == "cloud_essd" && nc.Spec.SystemDisk.PerformanceLevel == nil {
			perfLevel := "PL0"
			nc.Spec.SystemDisk.PerformanceLevel = &perfLevel
		}
	}

	// Set default DeleteWithInstance for data disks
	for i := range nc.Spec.DataDisks {
		if nc.Spec.DataDisks[i].DeleteWithInstance == nil {
			deleteWithInstance := true
			nc.Spec.DataDisks[i].DeleteWithInstance = &deleteWithInstance
		}
	}

	// Set default metadata options
	if nc.Spec.MetadataOptions == nil {
		hopLimit := int32(1)
		nc.Spec.MetadataOptions = &MetadataOptions{
			HttpTokens:              "optional",
			HttpPutResponseHopLimit: &hopLimit,
		}
	} else {
		if nc.Spec.MetadataOptions.HttpTokens == "" {
			nc.Spec.MetadataOptions.HttpTokens = "optional"
		}
		if nc.Spec.MetadataOptions.HttpPutResponseHopLimit == nil {
			hopLimit := int32(1)
			nc.Spec.MetadataOptions.HttpPutResponseHopLimit = &hopLimit
		}
	}

	// Set default capacity reservation preference
	if nc.Spec.CapacityReservationPreference == nil {
		pref := "open"
		nc.Spec.CapacityReservationPreference = &pref
	}

	return nil
}

// +kubebuilder:webhook:path=/validate-karpenter-alibabacloud-com-v1alpha1-ecsnodeclass,mutating=false,failurePolicy=fail,sideEffects=None,groups=karpenter.alibabacloud.com,resources=ecsnodeclasses,verbs=create;update,versions=v1alpha1,name=vecsnodeclass.kb.io,admissionReviewVersions=v1

var _ admission.CustomValidator = &ECSNodeClass{}

// ValidateCreate implements admission.CustomValidator so a webhook will be registered for the type
func (nc *ECSNodeClass) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	if err := nc.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}
	return nil, nil
}

// ValidateUpdate implements admission.CustomValidator so a webhook will be registered for the type
func (nc *ECSNodeClass) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	if err := nc.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Additional validation for updates
	oldNC, ok := oldObj.(*ECSNodeClass)
	if !ok {
		return nil, fmt.Errorf("old object is not an ECSNodeClass")
	}

	// Check if immutable fields are changed
	if err := nc.validateImmutableFields(oldNC); err != nil {
		return admission.Warnings{"Some fields cannot be changed after creation"}, err
	}

	return nil, nil
}

// ValidateDelete implements admission.CustomValidator so a webhook will be registered for the type
func (nc *ECSNodeClass) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	// No special validation needed for deletion
	return nil, nil
}

// validateImmutableFields validates that immutable fields are not changed
func (nc *ECSNodeClass) validateImmutableFields(old *ECSNodeClass) error {
	// Currently, all fields are mutable to allow updates
	// Add immutable field validation here if needed in the future
	return nil
}
