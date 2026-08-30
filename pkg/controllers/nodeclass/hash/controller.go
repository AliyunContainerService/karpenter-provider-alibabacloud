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

package hash

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Controller for computing and storing hashes of ECSNodeClass resources
type Controller struct {
	kubeClient client.Client
}

// NewController constructs a controller instance
func NewController(kubeClient client.Client) *Controller {
	return &Controller{
		kubeClient: kubeClient,
	}
}

// Reconcile the resource
func (c *Controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	nodeClass := &v1alpha1.ECSNodeClass{}
	if err := c.kubeClient.Get(ctx, req.NamespacedName, nodeClass); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Update the nodeclass with retry on conflict
	if err := c.updateNodeClass(ctx, nodeClass); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to update nodeclass: %w", err)
	}

	return reconcile.Result{}, nil
}

func computeHash(spec v1alpha1.ECSNodeClassSpec) (string, error) {
	type nodeClassHashInput struct {
		VSwitchSelectorTerms             []v1alpha1.VSwitchSelectorTerm             `json:"vSwitchSelectorTerms"`
		SecurityGroupSelectorTerms       []v1alpha1.SecurityGroupSelectorTerm       `json:"securityGroupSelectorTerms"`
		ImageSelectorTerms               []v1alpha1.ImageSelectorTerm               `json:"imageSelectorTerms"`
		UserData                         *string                                    `json:"userData,omitempty"`
		Kubelet                          *v1alpha1.KubeletConfiguration             `json:"kubelet,omitempty"`
		SystemDisk                       *v1alpha1.SystemDiskSpec                   `json:"systemDisk,omitempty"`
		DataDisks                        []v1alpha1.DataDiskSpec                    `json:"dataDisks,omitempty"`
		Tags                             map[string]string                          `json:"tags,omitempty"`
		Role                             *string                                    `json:"role,omitempty"`
		CapacityReservationPreference    *string                                    `json:"capacityReservationPreference,omitempty"`
		CapacityReservationSelectorTerms []v1alpha1.CapacityReservationSelectorTerm `json:"capacityReservationSelectorTerms,omitempty"`
	}

	hashInput := nodeClassHashInput{
		VSwitchSelectorTerms:             spec.VSwitchSelectorTerms,
		SecurityGroupSelectorTerms:       spec.SecurityGroupSelectorTerms,
		ImageSelectorTerms:               spec.ImageSelectorTerms,
		UserData:                         spec.UserData,
		Kubelet:                          spec.Kubelet,
		SystemDisk:                       spec.SystemDisk,
		DataDisks:                        spec.DataDisks,
		Tags:                             spec.Tags,
		Role:                             spec.Role,
		CapacityReservationPreference:    spec.CapacityReservationPreference,
		CapacityReservationSelectorTerms: spec.CapacityReservationSelectorTerms,
	}
	data, err := json.Marshal(hashInput)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:8]), nil
}

// updateNodeClass updates the nodeclass with retry on conflict
func (c *Controller) updateNodeClass(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get the latest version of the object
		latest := &v1alpha1.ECSNodeClass{}
		if err := c.kubeClient.Get(ctx, client.ObjectKeyFromObject(nodeClass), latest); err != nil {
			return err
		}

		hash, err := computeHash(latest.Spec)
		if err != nil {
			return fmt.Errorf("failed to compute hash: %w", err)
		}
		if latest.Annotations[v1alpha1.AnnotationECSNodeClassHash] == hash &&
			latest.Annotations[v1alpha1.AnnotationECSNodeClassHashVersion] == "v1" {
			return nil
		}

		if latest.Annotations == nil {
			latest.Annotations = map[string]string{}
		}
		latest.Annotations[v1alpha1.AnnotationECSNodeClassHash] = hash
		latest.Annotations[v1alpha1.AnnotationECSNodeClassHashVersion] = "v1"

		// Update the latest object
		return c.kubeClient.Update(ctx, latest)
	})
}

// Register the controller
func (c *Controller) Register(ctx context.Context, m manager.Manager) error {
	return builder.ControllerManagedBy(m).
		Named("nodeclass.hash").
		For(&v1alpha1.ECSNodeClass{}).
		Complete(c)
}
