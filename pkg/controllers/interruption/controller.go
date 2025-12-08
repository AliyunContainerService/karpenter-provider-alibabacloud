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

package interruption

import (
	"context"
	"fmt"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// Controller for handling interruption events
type Controller struct {
	kubeClient       client.Client
	cloudProvider    cloudprovider.CloudProvider
	instanceProvider *instance.Provider
	ecsClient        *ecs.Client
	clock            clock.Clock
}

// NewController constructs a controller instance
func NewController(kubeClient client.Client, cloudProvider cloudprovider.CloudProvider, instanceProvider *instance.Provider, ecsClient *ecs.Client, clock clock.Clock) *Controller {
	return &Controller{
		kubeClient:       kubeClient,
		cloudProvider:    cloudProvider,
		instanceProvider: instanceProvider,
		ecsClient:        ecsClient,
		clock:            clock,
	}
}

// Reconcile the resource
func (c *Controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	// Interruption handling is not implemented yet
	// Return early to avoid affecting main functionality
	log.FromContext(ctx).Info("Interruption handling is not implemented yet")
	return reconcile.Result{}, nil
}

func (c *Controller) markNodeForDeletion(ctx context.Context, node *corev1.Node) error {
	// Interruption handling is not implemented yet
	return fmt.Errorf("interruption handling is not implemented yet")
}

func (c *Controller) addInterruptionCondition(ctx context.Context, node *corev1.Node, interruptionTime string) error {
	// Interruption handling is not implemented yet
	return fmt.Errorf("interruption handling is not implemented yet")
}

// isInterrupted checks if an instance is interrupted
func (c *Controller) isInterrupted(inst *instance.Instance) (bool, string) {
	// Interruption handling is not implemented yet
	return false, "Interruption handling is not implemented yet"
}

// Register the controller
func (c *Controller) Register(ctx context.Context, m manager.Manager) error {
	return builder.ControllerManagedBy(m).
		Named("node.interruption").
		For(&corev1.Node{}).
		Complete(c)
}
