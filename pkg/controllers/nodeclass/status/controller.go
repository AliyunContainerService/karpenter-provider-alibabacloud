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
	"k8s.io/apimachinery/pkg/api/equality"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/launchtemplate"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/securitygroup"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/vswitch"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
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
	client                 client.Client
	vswitchProvider        *vswitch.Provider
	securityGroupProvider  *securitygroup.Provider
	imageFamilyProvider    *imagefamily.Provider
	launchTemplateProvider *launchtemplate.Provider
	ramProvider            *ramrole.Provider
}

const (
	successfulRequeueBase         = 15 * time.Minute
	successfulRequeueJitter       = 5 * time.Minute
	failedResolutionRequeueBase   = 5 * time.Minute
	failedResolutionRequeueJitter = time.Minute
)

const defaultSecurityGroupAttachLimit = 5

// NewController creates a new NodeClass status controller
func NewController(
	client client.Client,
	vswitchProvider *vswitch.Provider,
	securityGroupProvider *securitygroup.Provider,
	imageFamilyProvider *imagefamily.Provider,
	launchTemplateProvider *launchtemplate.Provider,
	ramProvider *ramrole.Provider,
) *Controller {
	return &Controller{
		client:                 client,
		vswitchProvider:        vswitchProvider,
		securityGroupProvider:  securityGroupProvider,
		imageFamilyProvider:    imageFamilyProvider,
		launchTemplateProvider: launchTemplateProvider,
		ramProvider:            ramProvider,
	}
}

// Reconcile updates the status of ECSNodeClass by resolving selectors
func (c *Controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	// Get the ECSNodeClass
	nodeClass := &v1alpha1.ECSNodeClass{}
	if err := c.client.Get(ctx, req.NamespacedName, nodeClass); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	stored := nodeClass.DeepCopy()
	var resolveErr error

	if nodeClass.Spec.LaunchTemplateID != nil {
		resolveErr = c.resolveLaunchTemplate(nodeClass, ctx)
	} else {
		resolveErr = c.resolveSelectorResources(nodeClass, ctx)
	}

	// Validate RAM Role
	if nodeClass.Spec.Role != nil {
		result := c.ramProvider.ValidateRole(ctx, *nodeClass.Spec.Role)
		if result != nil && !result.Valid {
			c.setCondition(nodeClass, v1alpha1.ConditionTypeRAMRoleResolved,
				metav1.ConditionFalse, result.Reason, result.Message)
			c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
				"ResourceResolutionFailed", "RAM role validation failed")
			if resolveErr == nil {
				resolveErr = fmt.Errorf("failed to validate RAM role: %s", result.Message)
			}
		} else {
			// Validation successful - set status
			nodeClass.Status.RAMRole = nodeClass.Spec.Role
			c.setCondition(nodeClass, v1alpha1.ConditionTypeRAMRoleResolved, metav1.ConditionTrue,
				"RAMRoleResolved", "RAM role is valid")
		}
	} else {
		// Validation successful - set status
		nodeClass.Status.RAMRole = nodeClass.Spec.Role
		c.setCondition(nodeClass, v1alpha1.ConditionTypeRAMRoleResolved, metav1.ConditionTrue,
			"RAMRoleResolved", "RAM role is valid")
	}

	// Set Ready condition based on other conditions
	if c.isReady(nodeClass) {
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionTrue,
			"Ready", "ECSNodeClass is ready")
	}

	// Update status if changed
	if !equality.Semantic.DeepEqual(stored, nodeClass) {
		if err := c.updateStatus(ctx, nodeClass); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to update ECSNodeClass status: %w", err)
		}
	}

	// If there was a resolve error, return it to trigger rate limiter backoff
	if resolveErr != nil {
		jitter := time.Duration(rand.Int63n(int64(failedResolutionRequeueJitter)))
		log.FromContext(ctx).Error(resolveErr, "failed to resolve ECSNodeClass resources, will retry after backoff", "requeueAfter", failedResolutionRequeueBase+jitter)
		return reconcile.Result{RequeueAfter: failedResolutionRequeueBase + jitter}, nil
	}

	// Add jitter to avoid synchronized updates with other controllers
	jitter := time.Duration(rand.Int63n(int64(successfulRequeueJitter)))
	return reconcile.Result{RequeueAfter: successfulRequeueBase + jitter}, nil
}

func (c *Controller) resolveLaunchTemplate(nodeClass *v1alpha1.ECSNodeClass, ctx context.Context) error {
	if c.launchTemplateProvider == nil {
		err := fmt.Errorf("launch template provider is required")
		c.setSelectorConditionsFromLaunchTemplateError(nodeClass, err)
		return err
	}
	data, err := c.launchTemplateProvider.GetData(ctx, *nodeClass.Spec.LaunchTemplateID, nodeClass.Spec.LaunchTemplateVersion)
	if err != nil {
		c.setSelectorConditionsFromLaunchTemplateError(nodeClass, err)
		return fmt.Errorf("failed to resolve launch template: %w", err)
	}
	if data.ImageID == "" || data.VSwitchID == "" || len(data.SecurityGroupIDs) == 0 {
		err := fmt.Errorf("launch template must specify image, vswitch, and security group")
		c.setSelectorConditionsFromLaunchTemplateError(nodeClass, err)
		return err
	}

	nodeClass.Status.Images = []v1alpha1.Image{{ID: data.ImageID}}
	vSwitchID := data.VSwitchID
	vswitches, err := c.vswitchProvider.Resolve(ctx, []v1alpha1.VSwitchSelectorTerm{{ID: &vSwitchID}})
	if err != nil {
		c.setCondition(nodeClass, v1alpha1.ConditionTypeVSwitchResolved, metav1.ConditionFalse,
			"LaunchTemplateResolutionFailed", err.Error())
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			"ResourceResolutionFailed", "Launch template VSwitch resolution failed")
		return fmt.Errorf("failed to resolve launch template vswitch: %w", err)
	}
	if len(vswitches) == 0 {
		err := fmt.Errorf("launch template vswitch not found")
		c.setCondition(nodeClass, v1alpha1.ConditionTypeVSwitchResolved, metav1.ConditionFalse,
			"LaunchTemplateResolutionFailed", err.Error())
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			"ResourceResolutionFailed", "Launch template VSwitch resolution failed")
		return err
	}
	nodeClass.Status.VSwitches = vswitches
	nodeClass.Status.SecurityGroups = make([]v1alpha1.SecurityGroup, 0, len(data.SecurityGroupIDs))
	for _, id := range data.SecurityGroupIDs {
		nodeClass.Status.SecurityGroups = append(nodeClass.Status.SecurityGroups, v1alpha1.SecurityGroup{ID: id})
	}
	c.setCondition(nodeClass, v1alpha1.ConditionTypeVSwitchResolved, metav1.ConditionTrue,
		"LaunchTemplateResolved", "Resolved VSwitch from launch template")
	c.setCondition(nodeClass, v1alpha1.ConditionTypeSecurityGroupResolved, metav1.ConditionTrue,
		"LaunchTemplateResolved", fmt.Sprintf("Resolved %d SecurityGroups from launch template", len(data.SecurityGroupIDs)))
	c.setCondition(nodeClass, v1alpha1.ConditionTypeImageResolved, metav1.ConditionTrue,
		"LaunchTemplateResolved", "Resolved Image from launch template")
	return nil
}

func (c *Controller) setSelectorConditionsFromLaunchTemplateError(nodeClass *v1alpha1.ECSNodeClass, err error) {
	message := err.Error()
	c.setCondition(nodeClass, v1alpha1.ConditionTypeVSwitchResolved, metav1.ConditionFalse,
		"LaunchTemplateResolutionFailed", message)
	c.setCondition(nodeClass, v1alpha1.ConditionTypeSecurityGroupResolved, metav1.ConditionFalse,
		"LaunchTemplateResolutionFailed", message)
	c.setCondition(nodeClass, v1alpha1.ConditionTypeImageResolved, metav1.ConditionFalse,
		"LaunchTemplateResolutionFailed", message)
	c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
		"ResourceResolutionFailed", "Launch template resolution failed")
}

func (c *Controller) resolveSelectorResources(nodeClass *v1alpha1.ECSNodeClass, ctx context.Context) error {
	var resolveErr error

	// Resolve VSwitches
	vswitches, err := c.vswitchProvider.Resolve(ctx, nodeClass.Spec.VSwitchSelectorTerms)
	if err != nil {
		c.setCondition(nodeClass, v1alpha1.ConditionTypeVSwitchResolved, metav1.ConditionFalse,
			"VSwitchResolutionFailed", err.Error())
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			"ResourceResolutionFailed", "VSwitch resolution failed")
		resolveErr = fmt.Errorf("failed to resolve vswitches: %w", err)
	} else if len(vswitches) == 0 {
		c.setCondition(nodeClass, v1alpha1.ConditionTypeVSwitchResolved, metav1.ConditionFalse,
			"VSwitchResolutionFailed", "vSwitchSelectorTerms resolved zero VSwitches")
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			"ResourceResolutionFailed", "VSwitch resolution failed")
		resolveErr = fmt.Errorf("no vswitches found matching selector terms")
	} else {
		nodeClass.Status.VSwitches = vswitches
		c.setCondition(nodeClass, v1alpha1.ConditionTypeVSwitchResolved, metav1.ConditionTrue,
			"VSwitchResolved", fmt.Sprintf("Resolved %d VSwitches", len(vswitches)))
	}

	// Resolve Security Groups
	securityGroups, err := c.securityGroupProvider.Resolve(ctx, nodeClass.Spec.SecurityGroupSelectorTerms)
	if err != nil {
		c.setCondition(nodeClass, v1alpha1.ConditionTypeSecurityGroupResolved, metav1.ConditionFalse,
			"SecurityGroupResolutionFailed", err.Error())
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			"ResourceResolutionFailed", "SecurityGroup resolution failed")
		if resolveErr == nil {
			resolveErr = fmt.Errorf("failed to resolve security groups: %w", err)
		}
	} else if len(securityGroups) == 0 {
		c.setCondition(nodeClass, v1alpha1.ConditionTypeSecurityGroupResolved, metav1.ConditionFalse,
			"SecurityGroupResolutionFailed", "securityGroupSelectorTerms resolved zero security groups")
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			"ResourceResolutionFailed", "SecurityGroup resolution failed")
		if resolveErr == nil {
			resolveErr = fmt.Errorf("no security groups found matching selector terms")
		}
	} else if err := validateResolvedSecurityGroupAttachLimit(securityGroups); err != nil {
		c.setCondition(nodeClass, v1alpha1.ConditionTypeSecurityGroupResolved, metav1.ConditionFalse,
			"SecurityGroupResolutionFailed", err.Error())
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			"ResourceResolutionFailed", "SecurityGroup resolution failed")
		if resolveErr == nil {
			resolveErr = err
		}
	} else {
		nodeClass.Status.SecurityGroups = securityGroups
		c.setCondition(nodeClass, v1alpha1.ConditionTypeSecurityGroupResolved, metav1.ConditionTrue,
			"SecurityGroupResolved", fmt.Sprintf("Resolved %d SecurityGroups", len(securityGroups)))
	}

	// Resolve Images
	images, err := c.imageFamilyProvider.Resolve(ctx, nodeClass.Spec.ImageSelectorTerms)
	if err != nil {
		c.setCondition(nodeClass, v1alpha1.ConditionTypeImageResolved, metav1.ConditionFalse,
			"ImageResolutionFailed", err.Error())
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			"ResourceResolutionFailed", "Image resolution failed")
		if resolveErr == nil {
			resolveErr = fmt.Errorf("failed to resolve images: %w", err)
		}
	} else if len(images) == 0 {
		c.setCondition(nodeClass, v1alpha1.ConditionTypeImageResolved, metav1.ConditionFalse,
			"ImageResolutionFailed", "imageSelectorTerms resolved zero images")
		c.setCondition(nodeClass, v1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			"ResourceResolutionFailed", "Image resolution failed")
		if resolveErr == nil {
			resolveErr = fmt.Errorf("no images found matching selector terms")
		}
	} else {
		nodeClass.Status.Images = images
		c.setCondition(nodeClass, v1alpha1.ConditionTypeImageResolved, metav1.ConditionTrue,
			"ImageResolved", fmt.Sprintf("Resolved %d Images", len(images)))
	}

	return resolveErr
}

// setCondition sets a condition on the ECSNodeClass
// LastTransitionTime is only updated when the status changes, following Kubernetes condition conventions
func (c *Controller) setCondition(nodeClass *v1alpha1.ECSNodeClass, conditionType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()

	// Find existing condition or create new one
	for i, condition := range nodeClass.Status.Conditions {
		if condition.Type == conditionType {
			// Status unchanged - only update reason and message, preserve LastTransitionTime
			if condition.Status == status {
				nodeClass.Status.Conditions[i].Reason = reason
				nodeClass.Status.Conditions[i].Message = message
				return
			}

			// Status changed - update all fields including LastTransitionTime
			nodeClass.Status.Conditions[i].Status = status
			nodeClass.Status.Conditions[i].Reason = reason
			nodeClass.Status.Conditions[i].Message = message
			nodeClass.Status.Conditions[i].LastTransitionTime = now
			return
		}
	}

	// Add new condition
	nodeClass.Status.Conditions = append(nodeClass.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
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

func validateResolvedSecurityGroupAttachLimit(securityGroups []v1alpha1.SecurityGroup) error {
	if len(securityGroups) < 1 {
		return fmt.Errorf("securityGroupSelectorTerms resolved zero security groups")
	}
	if len(securityGroups) > defaultSecurityGroupAttachLimit {
		return fmt.Errorf("securityGroupSelectorTerms resolved %d security groups, exceeding attach limit %d", len(securityGroups), defaultSecurityGroupAttachLimit)
	}
	return nil
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
		Named("ecsnodeclass.status").
		For(&v1alpha1.ECSNodeClass{}).
		WithOptions(controller.Options{
			// Use exponential backoff rate limiter for error retries
			// Max backoff of 5 minutes is suitable for cloud API rate limiting scenarios
			RateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](
				1*time.Second, // Initial backoff
				5*time.Minute, // Max backoff
			),
			MaxConcurrentReconciles: 1,
		}).
		Complete(c)
}

// safeErrorMessage returns the error message if err is not nil, otherwise returns a default message
func safeErrorMessage(err error, defaultMessage string) string {
	if err != nil {
		return err.Error()
	}
	return defaultMessage
}
