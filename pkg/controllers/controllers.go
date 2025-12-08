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

package controllers

import (
	"context"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/controllers/nodeclaim/garbagecollection"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/controllers/nodeclaim/tagging"
	"github.com/awslabs/operatorpkg/controller"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/controllers/nodeclass/hash"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/controllers/nodeclass/status"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator"
)

func NewControllers(
	ctx context.Context,
	alibabaOperator *operator.Operator,
) []controller.Controller {
	kubeClient := alibabaOperator.Manager.GetClient()

	var controllers []controller.Controller

	// NodeClass controllers
	controllers = append(controllers,
		// NodeClass status controller
		status.NewController(
			kubeClient,
			alibabaOperator.VSwitchProvider,
			alibabaOperator.SecurityGroupProvider,
			alibabaOperator.ImageFamilyProvider,
			alibabaOperator.RAMProvider,
		),
		// NodeClass hash controller for drift detection
		hash.NewController(kubeClient),
	)

	// NodeClaim controllers
	controllers = append(controllers,
		// NodeClaim garbage collection controller
		garbagecollection.NewGarbageCollectionController(
			kubeClient,
			alibabaOperator.CloudProvider,
			nil,
		),
		tagging.NewController(
			kubeClient,
			alibabaOperator.InstanceProvider,
		),
	)

	return controllers
}
