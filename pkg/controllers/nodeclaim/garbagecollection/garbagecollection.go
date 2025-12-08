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

package garbagecollection

import (
	"context"
	"fmt"
	"github.com/awslabs/operatorpkg/reconciler"
	"github.com/awslabs/operatorpkg/singleton"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
)

// GarbageCollectionController cleans up orphaned ECS instances
type GarbageCollectionController struct {
	client                     client.Client
	cloudProvider              cloudprovider.CloudProvider
	lastRun                    time.Time // Track last run time
	creationProtectionDuration time.Duration
}

// NewGarbageCollectionController creates a new garbage collection controller
func NewGarbageCollectionController(client client.Client, cloudProvider cloudprovider.CloudProvider, creationProtectionDuration *time.Duration) *GarbageCollectionController {
	if creationProtectionDuration == nil {
		creationProtectionDuration = lo.ToPtr(time.Minute * 1)
	}
	return &GarbageCollectionController{
		client:                     client,
		cloudProvider:              cloudProvider,
		lastRun:                    time.Now().Add(-5 * time.Minute), // Initialize to 5 minutes ago to ensure first run is not skipped
		creationProtectionDuration: *creationProtectionDuration,
	}
}

// Reconcile garbage collects orphaned instances
func (c *GarbageCollectionController) Reconcile(ctx context.Context) (reconciler.Result, error) {
	ctx = injection.WithControllerName(ctx, "instance.garbagecollection")

	// Check if less than 1 minute has passed since last run, skip if so to reduce API call frequency
	// Following AWS Karpenter defaults, reduce check interval from 2 minutes to 1 minute for faster response
	if time.Since(c.lastRun) < 1*time.Minute {
		log.FromContext(ctx).Info("Skipping garbage collection, not enough time since last run", "timeSinceLastRun", time.Since(c.lastRun))
		return reconciler.Result{RequeueAfter: 1*time.Minute - time.Since(c.lastRun)}, nil
	}

	// Update last run time
	c.lastRun = time.Now()

	log.FromContext(ctx).Info("Starting garbage collection cycle")

	// List all NodeClaims from the API server
	nodeClaimList := &coreapis.NodeClaimList{}
	if err := c.client.List(ctx, nodeClaimList); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list NodeClaims")
		return reconciler.Result{}, err
	}

	log.FromContext(ctx).Info("Listed NodeClaims", "count", len(nodeClaimList.Items))

	// Only proceed with garbage collection if there are NodeClaims to check
	if len(nodeClaimList.Items) == 0 {
		// No NodeClaims to check, requeue after a shorter interval
		// Following AWS Karpenter practice, reduce interval from 10 minutes to 5 minutes for faster response to changes
		log.FromContext(ctx).Info("No NodeClaims to check, requeuing")
		return reconciler.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// Get all instances from the cloud provider (single API call to fetch all instances)
	log.FromContext(ctx).Info("Listing cloud provider instances")
	cloudProviderNodeClaims, err := c.cloudProvider.List(ctx)
	if err != nil {
		// Log the error but don't return immediately, try again sooner
		// Following AWS Karpenter practice, reduce retry interval from 2 minutes to 1 minute
		log.FromContext(ctx).Error(err, "Failed to list cloud provider instances")
		return reconciler.Result{RequeueAfter: 1 * time.Minute}, fmt.Errorf("listing cloud provider instances, %w", err)
	}

	log.FromContext(ctx).Info("Listed cloud provider instances", "count", len(cloudProviderNodeClaims))

	// Create a map of cloud provider instances by provider ID for quick lookup
	cloudProviderNodeClaimsByID := map[string]*coreapis.NodeClaim{}
	for _, nodeClaim := range cloudProviderNodeClaims {
		log.FromContext(ctx).Info("Cloud provider instance", "providerID", nodeClaim.Status.ProviderID, "name", nodeClaim.Name)
		cloudProviderNodeClaimsByID[nodeClaim.Status.ProviderID] = nodeClaim
	}

	nodeList := &corev1.NodeList{}
	if err = c.client.List(ctx, nodeList); err != nil {
		log.FromContext(ctx).Error(err, "Failed to list nodes")
		return reconciler.Result{RequeueAfter: 5 * time.Minute}, fmt.Errorf("listing nodes: %w", err)
	}

	// Track if we found any orphaned NodeClaims
	foundOrphans := false

	// Iterate through all NodeClaims and check if they exist in the cloud provider
	for i := range nodeClaimList.Items {
		nodeClaim := &nodeClaimList.Items[i]

		log.FromContext(ctx).Info("Checking NodeClaim", "name", nodeClaim.Name, "providerID", nodeClaim.Status.ProviderID, "deletionTimestamp", nodeClaim.DeletionTimestamp)

		// If the NodeClaim doesn't exist in the cloud provider, it's an orphan and should be deleted
		if _, ok := cloudProviderNodeClaimsByID[nodeClaim.Status.ProviderID]; !ok {
			log.FromContext(ctx).Info("NodeClaim not found in cloud provider", "name", nodeClaim.Name, "providerID", nodeClaim.Status.ProviderID)

			// Check if the NodeClaim has been around long enough to be considered for garbage collection
			// This prevents race conditions where a NodeClaim is created but hasn't been launched in the cloud provider yet
			// Following AWS Karpenter practice, reduce creation protection period from 2 minutes to 1 minute for faster cleanup
			if time.Since(nodeClaim.CreationTimestamp.Time) < c.creationProtectionDuration {
				log.FromContext(ctx).Info("NodeClaim is too new, skipping garbage collection", "name", nodeClaim.Name, "age", time.Since(nodeClaim.CreationTimestamp.Time))
				continue
			}

			// Log the orphaned NodeClaim for debugging
			log.FromContext(ctx).Info("Found orphaned NodeClaim", "name", nodeClaim.Name, "providerID", nodeClaim.Status.ProviderID)

			// Delete the orphaned NodeClaim
			if err := c.cloudProvider.Delete(ctx, nodeClaim); cloudprovider.IgnoreNodeClaimNotFoundError(err) != nil {
				log.FromContext(ctx).Error(err, "failed to delete orphaned nodeclaim", "name", nodeClaim.Name)
				// Continue with other NodeClaims rather than stopping the entire process
				continue
			}

			// Go ahead and cleanup the node if we know that it exists to make scheduling go quicker
			_, nodeIdx, ok := lo.FindIndexOf(nodeList.Items, func(n corev1.Node) bool {
				return n.Spec.ProviderID == nodeClaim.Status.ProviderID
			})
			if ok {
				node := &nodeList.Items[nodeIdx]
				if err := c.client.Delete(ctx, node); err != nil && client.IgnoreNotFound(err) != nil {
					log.FromContext(ctx).Error(err, "failed to delete orphaned node", "name", node.Name)
					continue
				}
				log.FromContext(ctx).Info("Deleted orphaned node", "name", node.Name)
			}

			// Mark that we found orphans
			foundOrphans = true
		} else {
			log.FromContext(ctx).Info("NodeClaim found in cloud provider, not an orphan", "name", nodeClaim.Name, "providerID", nodeClaim.Status.ProviderID)
		}
	}

	// If we found orphans, requeue sooner to check again
	// Following AWS Karpenter practice, reduce retry interval from 2 minutes to 1 minute
	if foundOrphans {
		log.FromContext(ctx).Info("Found orphans, requeuing sooner")
		return reconciler.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	// Requeue after 10 minutes to check for orphaned instances again
	// This reduces the frequency of API calls to prevent throttling
	// Following AWS Karpenter practice, reduce interval from 10 minutes to 5 minutes for faster response to changes
	log.FromContext(ctx).Info("Garbage collection cycle completed, requeuing")
	return reconciler.Result{RequeueAfter: 5 * time.Minute}, nil
}

// Register registers the controller with the manager
func (c *GarbageCollectionController) Register(ctx context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("instance.garbagecollection").
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(c))
}
