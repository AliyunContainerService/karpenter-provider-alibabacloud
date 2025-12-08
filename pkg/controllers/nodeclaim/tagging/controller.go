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
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// Controller for tagging instances with Karpenter metadata
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

	// Get the nodeclass
	nodeClass := &v1alpha1.ECSNodeClass{}
	if err := c.kubeClient.Get(ctx, client.ObjectKey{Name: nodeClaim.Spec.NodeClassRef.Name}, nodeClass); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Extract instance ID from provider ID
	instanceID := getInstanceIDFromProviderID(nodeClaim.Status.ProviderID)
	if instanceID == "" {
		return reconcile.Result{}, fmt.Errorf("failed to extract instance ID from provider ID: %s", nodeClaim.Status.ProviderID)
	}

	// Build tags
	tags := buildTags(nodeClaim, nodeClass)

	// Apply tags to the instance
	if err := c.instanceProvider.TagInstance(ctx, instanceID, tags); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to tag instance %s: %w", instanceID, err)
	}

	return reconcile.Result{}, nil
}

func getInstanceIDFromProviderID(providerID string) string {
	// ProviderID format: cn-hangzhou.i-xxxxx
	// Extract the instance ID (last part after the last '.')

	// Split by '.'
	parts := strings.Split(providerID, ".")
	if len(parts) == 0 {
		return ""
	}

	// Return the last part which is the instance ID
	return parts[len(parts)-1]
}

func buildTags(nodeClaim *coreapis.NodeClaim, nodeClass *v1alpha1.ECSNodeClass) map[string]string {
	tags := map[string]string{
		"karpenter.sh/nodeclaim":  nodeClaim.Name,
		"karpenter.sh/nodepool":   nodeClaim.Labels[coreapis.NodePoolLabelKey],
		"karpenter.sh/managed-by": "karpenter",
	}

	// Add custom tags from nodeclass
	for k, v := range nodeClass.Spec.Tags {
		tags[k] = v
	}

	// Add labels from nodeclaim
	for k, v := range nodeClaim.Labels {
		tags[k] = v
	}

	return tags
}

// Register the controller
func (c *Controller) Register(ctx context.Context, m manager.Manager) error {
	return builder.ControllerManagedBy(m).
		Named("nodeclaim.tagging").
		For(&coreapis.NodeClaim{}).
		Complete(c)
}
