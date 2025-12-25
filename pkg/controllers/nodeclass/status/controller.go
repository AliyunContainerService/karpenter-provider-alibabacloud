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

package status

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/ramrole"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/securitygroup"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/vswitch"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Controller reconciles ECSNodeClass status
// This controller is separate from the main controller to avoid conflicts
type Controller struct {
	client                client.Client
	vswitchProvider       *vswitch.Provider
	securityGroupProvider *securitygroup.Provider
	imageFamilyProvider   *imagefamily.Provider
	ramProvider           *ramrole.Provider
}

// NewController creates a new NodeClass status controller
func NewController(
	client client.Client,
	vswitchProvider *vswitch.Provider,
	securityGroupProvider *securitygroup.Provider,
	imageFamilyProvider *imagefamily.Provider,
	ramProvider *ramrole.Provider,
) *Controller {
	return &Controller{
		client:                client,
		vswitchProvider:       vswitchProvider,
		securityGroupProvider: securityGroupProvider,
		imageFamilyProvider:   imageFamilyProvider,
		ramProvider:           ramProvider,
	}
}

// Reconcile updates the status of ECSNodeClass by resolving selectors
func (c *Controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := log.FromContext(ctx)

	// Get the ECSNodeClass
	nodeClass := &v1alpha1.ECSNodeClass{}
	if err := c.client.Get(ctx, req.NamespacedName, nodeClass); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// Track if any updates were made
	statusChanged := false

	// Resolve VSwitches
	vswitches, err := c.vswitchProvider.Resolve(ctx, nodeClass.Spec.VSwitchSelectorTerms)
	if err != nil || len(vswitches) == 0 {
		log.Error(err, "failed to resolve vswitches", "nodeclass", nodeClass.Name)
		c.setCondition(nodeClass, v1alpha1.ConditionTypeVSwitchResolved, metav1.ConditionFalse,
			"VSwitchResolutionFailed", safeErrorMessage(err, "vswitch not found"))
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			"ResourceResolutionFailed", "VSwitch resolution failed")
		statusChanged = true
	} else {
		nodeClass.Status.VSwitches = vswitches
		c.setCondition(nodeClass, v1alpha1.ConditionTypeVSwitchResolved, metav1.ConditionTrue,
			"VSwitchResolved", fmt.Sprintf("Resolved %d VSwitches", len(vswitches)))
		statusChanged = true
	}

	// Resolve Security Groups
	securityGroups, err := c.securityGroupProvider.Resolve(ctx, nodeClass.Spec.SecurityGroupSelectorTerms)
	if err != nil || len(securityGroups) == 0 {
		log.Error(err, "failed to resolve security groups", "nodeclass", nodeClass.Name)
		c.setCondition(nodeClass, v1alpha1.ConditionTypeSecurityGroupResolved, metav1.ConditionFalse,
			"SecurityGroupResolutionFailed", safeErrorMessage(err, "security group not found"))
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			"ResourceResolutionFailed", "SecurityGroup resolution failed")
		statusChanged = true
	} else {
		nodeClass.Status.SecurityGroups = securityGroups
		c.setCondition(nodeClass, v1alpha1.ConditionTypeSecurityGroupResolved, metav1.ConditionTrue,
			"SecurityGroupResolved", fmt.Sprintf("Resolved %d SecurityGroups", len(securityGroups)))
		statusChanged = true
	}

	// Resolve Images
	images, err := c.imageFamilyProvider.Resolve(ctx, nodeClass.Spec.ImageSelectorTerms)
	if err != nil || len(images) == 0 {
		log.Error(err, "failed to resolve images", "nodeclass", nodeClass.Name)
		c.setCondition(nodeClass, v1alpha1.ConditionTypeImageResolved, metav1.ConditionFalse,
			"ImageResolutionFailed", safeErrorMessage(err, "image not found"))
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			"ResourceResolutionFailed", "Image resolution failed")
		statusChanged = true
	} else {
		nodeClass.Status.Images = images
		c.setCondition(nodeClass, v1alpha1.ConditionTypeImageResolved, metav1.ConditionTrue,
			"ImageResolved", fmt.Sprintf("Resolved %d Images", len(images)))
		statusChanged = true
	}

	// Validate RAM Role
	if nodeClass.Spec.Role != nil {
		result := c.ramProvider.ValidateRole(ctx, *nodeClass.Spec.Role)
		if result != nil && !result.Valid {
			c.setCondition(nodeClass, v1alpha1.ConditionTypeRAMRoleResolved,
				metav1.ConditionFalse, result.Reason, result.Message)
			c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
				"ResourceResolutionFailed", "RAM role validation failed")
			statusChanged = true
		} else {
			// Validation successful - set status
			nodeClass.Status.RAMRole = nodeClass.Spec.Role
			c.setCondition(nodeClass, v1alpha1.ConditionTypeRAMRoleResolved, metav1.ConditionTrue,
				"RAMRoleResolved", "RAM role is valid")
			statusChanged = true
		}
	} else {
		// Validation successful - set status
		nodeClass.Status.RAMRole = nodeClass.Spec.Role
		c.setCondition(nodeClass, v1alpha1.ConditionTypeRAMRoleResolved, metav1.ConditionTrue,
			"RAMRoleResolved", "RAM role is valid")
		statusChanged = true
	}

	// Set Ready condition based on other conditions
	if c.isReady(nodeClass) {
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionTrue,
			"Ready", "ECSNodeClass is ready")
		statusChanged = true
	}

	// Update status if changed
	if statusChanged {
		if err := c.updateStatus(ctx, nodeClass); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to update ECSNodeClass status: %w", err)
		}
	}

	// Add jitter to avoid synchronized updates with other controllers
	jitter := time.Duration(rand.Int63n(300)) * time.Second // 0-5 minutes random jitter
	return reconcile.Result{RequeueAfter: 15*time.Minute + jitter}, nil
}

// setCondition sets a condition on the ECSNodeClass
func (c *Controller) setCondition(nodeClass *v1alpha1.ECSNodeClass, conditionType string, status metav1.ConditionStatus, reason, message string) {
	// Find existing condition or create new one
	for i, condition := range nodeClass.Status.Conditions {
		if condition.Type == conditionType {
			// Update existing condition
			nodeClass.Status.Conditions[i].Status = status
			nodeClass.Status.Conditions[i].Reason = reason
			nodeClass.Status.Conditions[i].Message = message
			nodeClass.Status.Conditions[i].LastTransitionTime = metav1.Now()
			return
		}
	}

	// Add new condition
	nodeClass.Status.Conditions = append(nodeClass.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})
}

// isReady checks if all required conditions are met
func (c *Controller) isReady(nodeClass *v1alpha1.ECSNodeClass) bool {
	requiredConditions := []string{
		v1alpha1.ConditionTypeVSwitchResolved,
		v1alpha1.ConditionTypeSecurityGroupResolved,
		v1alpha1.ConditionTypeImageResolved,
		v1alpha1.ConditionTypeRAMRoleResolved,
	}

	for _, conditionType := range requiredConditions {
		condition := c.getCondition(nodeClass, conditionType)
		if condition == nil || condition.Status != metav1.ConditionTrue {
			return false
		}
	}

	return true
}

// getCondition gets a condition from the ECSNodeClass
func (c *Controller) getCondition(nodeClass *v1alpha1.ECSNodeClass, conditionType string) *metav1.Condition {
	for _, condition := range nodeClass.Status.Conditions {
		if condition.Type == conditionType {
			return &condition
		}
	}
	return nil
}

// updateStatus updates the status of the ECSNodeClass with retry on conflict
func (c *Controller) updateStatus(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		log := log.FromContext(ctx)

		// Get the latest version of the object
		latest := &v1alpha1.ECSNodeClass{}
		if err := c.client.Get(ctx, client.ObjectKeyFromObject(nodeClass), latest); err != nil {
			log.Error(err, "failed to get latest ECSNodeClass for status update", "nodeclass", nodeClass.Name)
			return err
		}

		// Copy the status to the latest object
		latest.Status = nodeClass.Status

		// Update the latest object
		err := c.client.Status().Update(ctx, latest)
		if err != nil {
			log.Error(err, "failed to update ECSNodeClass status, will retry", "nodeclass", nodeClass.Name)
		} else {
			log.V(1).Info("successfully updated ECSNodeClass status", "nodeclass", nodeClass.Name)
		}
		return err
	})
}

// Register registers the controller with the manager
func (c *Controller) Register(ctx context.Context, mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ECSNodeClass{}).
		Complete(c)
}

// safeErrorMessage returns the error message if err is not nil, otherwise returns a default message
func safeErrorMessage(err error, defaultMessage string) string {
	if err != nil {
		return err.Error()
	}
	return defaultMessage
}
