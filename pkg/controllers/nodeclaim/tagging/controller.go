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

package tagging

import (
	"context"
	"fmt"
	"strings"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/cloudprovider"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// Controller for tagging instances with Karpenter metadata.
// This is a one-time initialization controller: it tags the instance once,
// then writes an annotation to the NodeClaim to prevent redundant API calls.
// This aligns with AWS Karpenter's design where tagging is a one-time operation.
type Controller struct {
	kubeClient       client.Client
	instanceProvider *instance.Provider
}

// NewController constructs a controller instance
func NewController(kubeClient client.Client, instanceProvider *instance.Provider) *Controller {
	return &Controller{
		kubeClient:       kubeClient,
		instanceProvider: instanceProvider,
	}
}

// Reconcile the resource
func (c *Controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	nodeClaim := &coreapis.NodeClaim{}
	if err := c.kubeClient.Get(ctx, req.NamespacedName, nodeClaim); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Check if already tagged (double-check beyond predicate)
	if !isTaggable(nodeClaim) {
		return reconcile.Result{}, nil
	}

	// Wait for ProviderID to be assigned
	if nodeClaim.Status.ProviderID == "" {
		return reconcile.Result{}, nil
	}

	// Skip if being deleted
	if !nodeClaim.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	// Deep copy for patch
	stored := nodeClaim.DeepCopy()

	// Get the nodeclass
	nodeClass := &v1alpha1.ECSNodeClass{}
	if err := c.kubeClient.Get(ctx, client.ObjectKey{Name: nodeClaim.Spec.NodeClassRef.Name}, nodeClass); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Extract instance ID from provider ID
	instanceID := getInstanceIDFromProviderID(nodeClaim.Status.ProviderID)
	if instanceID == "" {
		return reconcile.Result{}, nil // Don't error, wait for ProviderID
	}

	// Tag instance with diff logic
	if err := c.tagInstance(ctx, nodeClaim, nodeClass, instanceID); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to tag instance %s: %w", instanceID, err)
	}

	// Write annotation to mark tagging as complete
	if nodeClaim.Annotations == nil {
		nodeClaim.Annotations = make(map[string]string)
	}
	nodeClaim.Annotations[v1alpha1.AnnotationInstanceTagged] = "true"

	// Patch the NodeClaim
	if !equality.Semantic.DeepEqual(nodeClaim, stored) {
		if err := c.kubeClient.Patch(ctx, nodeClaim, client.MergeFrom(stored)); err != nil {
			return reconcile.Result{}, client.IgnoreNotFound(err)
		}
	}

	return reconcile.Result{}, nil
}

// tagInstance performs diff-based tagging: only tags that are missing or different
// on the instance will be applied. This avoids redundant API calls.
func (c *Controller) tagInstance(ctx context.Context, nodeClaim *coreapis.NodeClaim, nodeClass *v1alpha1.ECSNodeClass, instanceID string) error {
	// Build desired tags (uses the same function as Create for consistency)
	desiredTags := cloudprovider.BuildInstanceTags(nodeClaim, nodeClass)
	// Storage tags describe the disk and reservations at launch. A later
	// NodeClass edit must not rewrite them on an existing instance.
	delete(desiredTags, v1alpha1.TagEphemeralStorageCapacity)
	delete(desiredTags, v1alpha1.TagEphemeralStorageAllocatable)

	// Get current instance tags, bypassing cache for accuracy
	inst, err := c.instanceProvider.Get(ctx, instanceID, true /* skipCache */)
	if err != nil {
		return fmt.Errorf("getting instance for tagging: %w", err)
	}

	// Diff: only tag keys that are missing or have different values
	tagsToApply := make(map[string]string)
	for k, v := range desiredTags {
		if currentV, exists := inst.Tags[k]; !exists || currentV != v {
			tagsToApply[k] = v
		}
	}

	// No tags to apply
	if len(tagsToApply) == 0 {
		return nil
	}

	// TagInstance internally handles batching for >20 tags
	return c.instanceProvider.TagInstance(ctx, instanceID, tagsToApply)
}

// isTaggable returns false if the NodeClaim has already been tagged or is not ready
func isTaggable(nc *coreapis.NodeClaim) bool {
	// Already tagged
	if nc.Annotations[v1alpha1.AnnotationInstanceTagged] == "true" {
		return false
	}
	// ProviderID not yet assigned
	if nc.Status.ProviderID == "" {
		return false
	}
	// Being deleted
	if !nc.DeletionTimestamp.IsZero() {
		return false
	}
	return true
}

func getInstanceIDFromProviderID(providerID string) string {
	// ProviderID format: cn-hangzhou.i-xxxxx or alibabacloud://cn-beijing.i-xxxxx
	// Extract the instance ID (last part after the last '.')
	parts := strings.Split(providerID, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// Register the controller with predicate filter
func (c *Controller) Register(ctx context.Context, m manager.Manager) error {
	return builder.ControllerManagedBy(m).
		Named("nodeclaim.tagging").
		For(&coreapis.NodeClaim{}).
		WithEventFilter(predicate.NewPredicateFuncs(func(o client.Object) bool {
			return isTaggable(o.(*coreapis.NodeClaim))
		})).
		Complete(c)
}
