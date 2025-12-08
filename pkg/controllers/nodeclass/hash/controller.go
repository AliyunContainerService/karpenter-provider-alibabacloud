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
	"crypto/md5"
	"fmt"
	"sort"

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

	// Compute hash of the nodeclass spec
	hash, err := computeHash(nodeClass.Spec)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to compute hash: %w", err)
	}

	// Store hash in annotation
	if nodeClass.Annotations == nil {
		nodeClass.Annotations = map[string]string{}
	}
	nodeClass.Annotations[v1alpha1.AnnotationECSNodeClassHash] = hash

	// Update the nodeclass with retry on conflict
	if err := c.updateNodeClass(ctx, nodeClass); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to update nodeclass: %w", err)
	}

	return reconcile.Result{}, nil
}

func computeHash(spec v1alpha1.ECSNodeClassSpec) (string, error) {
	// Create a consistent string representation of the spec
	// We need to ensure the same spec always produces the same hash
	// regardless of map ordering or other non-deterministic factors

	// For VSwitch selector terms, sort by ID for consistency
	vswitchTerms := make([]string, len(spec.VSwitchSelectorTerms))
	for i, term := range spec.VSwitchSelectorTerms {
		if term.ID != nil {
			vswitchTerms[i] = *term.ID
		} else {
			vswitchTerms[i] = ""
		}
	}
	sort.Strings(vswitchTerms)

	// For Security Group selector terms, sort by ID for consistency
	sgTerms := make([]string, len(spec.SecurityGroupSelectorTerms))
	for i, term := range spec.SecurityGroupSelectorTerms {
		if term.ID != nil {
			sgTerms[i] = *term.ID
		} else {
			sgTerms[i] = ""
		}
	}
	sort.Strings(sgTerms)

	// For Image selector terms, sort by ID for consistency
	imageTerms := make([]string, len(spec.ImageSelectorTerms))
	for i, term := range spec.ImageSelectorTerms {
		if term.ID != nil {
			imageTerms[i] = *term.ID
		} else {
			imageTerms[i] = ""
		}
	}
	sort.Strings(imageTerms)

	// Build a consistent string representation
	str := fmt.Sprintf("%v|%v|%v|%v",
		vswitchTerms,
		sgTerms,
		imageTerms,
		spec.Role)

	// Compute MD5 hash
	h := md5.Sum([]byte(str))
	return fmt.Sprintf("%x", h), nil
}

// updateNodeClass updates the nodeclass with retry on conflict
func (c *Controller) updateNodeClass(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get the latest version of the object
		latest := &v1alpha1.ECSNodeClass{}
		if err := c.kubeClient.Get(ctx, client.ObjectKeyFromObject(nodeClass), latest); err != nil {
			return err
		}

		// Copy the annotations to the latest object
		if latest.Annotations == nil {
			latest.Annotations = map[string]string{}
		}
		for k, v := range nodeClass.Annotations {
			latest.Annotations[k] = v
		}

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
