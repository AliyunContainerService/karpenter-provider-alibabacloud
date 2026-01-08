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

package main

import (
	"fmt"
	"os"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/cloudprovider"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/controllers"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator/options"

	"sigs.k8s.io/karpenter/pkg/cloudprovider/metrics"
	"sigs.k8s.io/karpenter/pkg/cloudprovider/overlay"
	corecontrollers "sigs.k8s.io/karpenter/pkg/controllers"
	"sigs.k8s.io/karpenter/pkg/controllers/state"
	coreoperator "sigs.k8s.io/karpenter/pkg/operator"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
)

func main() {
	// Add our custom options to the injectables
	coreoptions.Injectables = append(coreoptions.Injectables, options.New())

	// Create core operator
	ctx, coreOp := coreoperator.NewOperator()

	// Create Alibaba Cloud operator with providers, passing the core operator
	alibabaOperator, err := operator.NewOperator(ctx, coreOp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Alibaba Cloud operator: %v\n", err)
		os.Exit(1)
	}

	alibabaCloudProvider := cloudprovider.New(
		alibabaOperator.InstanceTypeProvider,
		alibabaOperator.InstanceProvider,
		alibabaOperator.EventRecorder,
		alibabaOperator.Manager.GetClient(),
		alibabaOperator.ImageFamilyProvider,
		alibabaOperator.SecurityGroupProvider,
		alibabaOperator.VSwitchProvider,
		alibabaOperator.InstanceProfileProvider,
		alibabaOperator.PricingProvider,
		alibabaOperator.LaunchTemplateProvider,
		alibabaOperator.BootstrapProvider,
		alibabaOperator.ClusterNetworkConfig,
	)

	overlayUndecoratedCloudProvider := metrics.Decorate(alibabaCloudProvider)
	cloudProvider := overlay.Decorate(overlayUndecoratedCloudProvider, coreOp.Manager.GetClient(), coreOp.InstanceTypeStore)
	clusterState := state.NewCluster(coreOp.Clock, coreOp.Manager.GetClient(), cloudProvider)

	// Set the CloudProvider in the Alibaba operator
	alibabaOperator.CloudProvider = cloudProvider

	coreOp.
		WithControllers(ctx, corecontrollers.NewControllers(
			ctx,
			coreOp.Manager,
			coreOp.Clock,
			coreOp.Manager.GetClient(),
			coreOp.EventRecorder,
			cloudProvider,
			overlayUndecoratedCloudProvider,
			clusterState,
			coreOp.InstanceTypeStore,
		)...).
		WithControllers(ctx, controllers.NewControllers(ctx, alibabaOperator)...).
		Start(ctx)
}
