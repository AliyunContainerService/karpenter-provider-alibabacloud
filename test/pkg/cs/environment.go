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

package cs

import (
	"context"
	"fmt"
	"github.com/aws/karpenter-provider-aws/test/pkg/debug"
	"k8s.io/apimachinery/pkg/types"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator"
	"github.com/awslabs/operatorpkg/object"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/karpenter/pkg/test"
	"sigs.k8s.io/karpenter/pkg/utils/pod"

	"github.com/aws/karpenter-provider-aws/test/pkg/environment/common"

	"github.com/samber/lo"
	"k8s.io/client-go/kubernetes/scheme"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func init() {
	ctrl.SetLogger(zap.New())
	lo.Must0(v1alpha1.AddToScheme(scheme.Scheme)) // add scheme for the security group policy CRD
	karpv1.NormalizedLabels = lo.Assign(karpv1.NormalizedLabels)
}

var persistedSettings []corev1.EnvVar

var DefaultImageID = "aliyun_4_x64_20G_container_optimized_alibase_20251106.vhd"

var (
	CleanableObjects = []client.Object{
		&corev1.Pod{},
		&appsv1.Deployment{},
		&appsv1.StatefulSet{},
		&appsv1.DaemonSet{},
		&policyv1.PodDisruptionBudget{},
		&corev1.PersistentVolumeClaim{},
		&corev1.PersistentVolume{},
		&storagev1.StorageClass{},
		&karpv1.NodePool{},
		&corev1.LimitRange{},
		&schedulingv1.PriorityClass{},
		&corev1.Node{},
		&karpv1.NodeClaim{},
		&v1alpha1.ECSNodeClass{},
	}
)

type Environment struct {
	*common.Environment
	Region string

	ECSAPI  clients.ECSClient
	VPCAPI  clients.VPCClient
	CSAPI   clients.CSClient
	RAMMAPI clients.RAMClient

	ClusterID       string
	ClusterName     string
	ClusterEndpoint string
	ZoneInfo        []ZoneInfo
}

type ZoneInfo struct {
	Zone     string
	ZoneID   string
	ZoneType string
}

type TestConfig struct {
	Region          string
	AccessKeyID     string
	AccessKeySecret string
}

func NewEnvironment(t *testing.T) *Environment {
	env := common.NewEnvironment(t)
	cfg := TestConfig{
		Region:          lo.Must(os.LookupEnv("TEST_REGION")),
		AccessKeyID:     lo.Must(os.LookupEnv("ALIBABA_CLOUD_ACCESS_KEY_ID")),
		AccessKeySecret: lo.Must(os.LookupEnv("ALIBABA_CLOUD_ACCESS_KEY_SECRET")),
	}

	csAPI, err := operator.InitCSClient(cfg.Region, cfg.AccessKeyID, cfg.AccessKeySecret)
	Expect(err).ToNot(HaveOccurred())
	ecsAPI, err := operator.InitECSClient(cfg.Region, cfg.AccessKeyID, cfg.AccessKeySecret)
	Expect(err).ToNot(HaveOccurred())
	vpcAPI, err := operator.InitVPCClient(cfg.Region, cfg.AccessKeyID, cfg.AccessKeySecret)
	Expect(err).ToNot(HaveOccurred())
	ramAPI, err := operator.InitRAMClient(cfg.Region, cfg.AccessKeyID, cfg.AccessKeySecret)
	Expect(err).ToNot(HaveOccurred())

	testEnv := &Environment{
		Region:          cfg.Region,
		Environment:     env,
		ECSAPI:          ecsAPI,
		VPCAPI:          vpcAPI,
		CSAPI:           csAPI,
		RAMMAPI:         ramAPI,
		ClusterName:     lo.Must(os.LookupEnv("TEST_CLUSTER_NAME")),
		ClusterID:       lo.Must(os.LookupEnv("TEST_CLUSTER_ID")),
		ClusterEndpoint: lo.Must(os.LookupEnv("TEST_CLUSTER_ENDPOINT")),
	}
	return testEnv
}

func (env *Environment) BeforeEach() {
	d := &appsv1.Deployment{}
	Expect(env.Client.Get(env.Context, types.NamespacedName{Namespace: "karpenter", Name: "karpenter"}, d)).To(Succeed())
	Expect(d.Spec.Template.Spec.Containers).To(HaveLen(1))
	persistedSettings = lo.Map(d.Spec.Template.Spec.Containers[0].Env, func(v corev1.EnvVar, _ int) corev1.EnvVar {
		return *v.DeepCopy()
	})
	debug.BeforeEach(env.Context, env.Config, env.Client)

	// 删除已有的 ECSNodeClass 和 NodePool 对象，确保测试环境干净
	nodeClassList := &v1alpha1.ECSNodeClassList{}
	if err := env.Client.List(context.Background(), nodeClassList); err == nil {
		for i := range nodeClassList.Items {
			_ = env.Client.Delete(context.Background(), &nodeClassList.Items[i], &client.DeleteOptions{})
		}
	}
	nodePoolList := &karpv1.NodePoolList{}
	if err := env.Client.List(context.Background(), nodePoolList); err == nil {
		for i := range nodePoolList.Items {
			_ = env.Client.Delete(context.Background(), &nodePoolList.Items[i], &client.DeleteOptions{})
		}
	}

	// 给已有节点打上 taint，防止测试 Pod 调度到现有节点
	nodeList := &corev1.NodeList{}
	Expect(env.Client.List(context.Background(), nodeList)).To(Succeed())
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		// 检查节点是否已有该 taint
		hasTaint := false
		for _, taint := range node.Spec.Taints {
			if taint.Key == "karpenter-test" {
				hasTaint = true
				break
			}
		}
		if !hasTaint {
			node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{
				Key:    "karpenter-test",
				Value:  "true",
				Effect: corev1.TaintEffectNoSchedule,
			})
			Expect(env.Client.Update(context.Background(), node, &client.UpdateOptions{})).To(Succeed())
		}
	}

	// Expect this cluster to be clean for test runs to execute successfully
	env.ValidateCleanEnvironment()

	env.Monitor.Reset()
	env.StartingNodeCount = env.Monitor.NodeCountAtReset()
}

func (env *Environment) AfterEach() {
	env.CleanupObjects(CleanableObjects...)
	env.Environment.AfterEach()
}

func (env *Environment) ValidateCleanEnvironment() {
	var nodes corev1.NodeList
	Expect(env.Client.List(env.Context, &nodes)).To(Succeed())
	for _, node := range nodes.Items {
		if len(node.Spec.Taints) == 0 && !node.Spec.Unschedulable {
			Fail(fmt.Sprintf("expected system pool node %s to be tainted", node.Name))
		}
	}
	var pods corev1.PodList
	Expect(env.Client.List(env.Context, &pods)).To(Succeed())
	for i := range pods.Items {
		Expect(pod.IsProvisionable(&pods.Items[i])).To(BeFalse(),
			fmt.Sprintf("expected to have no provisionable pods, found %s/%s", pods.Items[i].Namespace, pods.Items[i].Name))
		Expect(pods.Items[i].Namespace).ToNot(Equal("default"),
			fmt.Sprintf("expected no pods in the `default` namespace, found %s/%s", pods.Items[i].Namespace, pods.Items[i].Name))
	}
	for _, obj := range []client.Object{&karpv1.NodePool{}, &v1alpha1.ECSNodeClass{}} {
		metaList := &metav1.PartialObjectMetadataList{}
		gvk := lo.Must(apiutil.GVKForObject(obj, env.Client.Scheme()))
		metaList.SetGroupVersionKind(gvk)
		Expect(env.Client.List(env.Context, metaList, client.Limit(1))).To(Succeed())
		Expect(metaList.Items).To(HaveLen(0), fmt.Sprintf("expected no %s to exist", gvk.Kind))
	}
}

func (env *Environment) DefaultECSNodeClass() *v1alpha1.ECSNodeClass {
	nodeClass := &v1alpha1.ECSNodeClass{}

	nodeClass.ObjectMeta = test.ObjectMeta(nodeClass.ObjectMeta)

	nodeClass.Spec.ClusterID = env.ClusterID
	nodeClass.Spec.Tags = map[string]string{
		"testing/cluster": env.ClusterName,
	}
	nodeClass.Spec.SecurityGroupSelectorTerms = []v1alpha1.SecurityGroupSelectorTerm{
		{
			Tags: map[string]string{"karpenter.sh/discovery": env.ClusterName},
		},
	}
	nodeClass.Spec.VSwitchSelectorTerms = []v1alpha1.VSwitchSelectorTerm{
		{
			Tags: map[string]string{"karpenter.sh/discovery": env.ClusterName},
		},
	}
	nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{
		{
			ID: &DefaultImageID,
		},
	}
	nodeClass.Spec.SystemDisk = &v1alpha1.SystemDiskSpec{
		Category:         "cloud_essd",
		Size:             lo.ToPtr(int32(40)),
		PerformanceLevel: lo.ToPtr("PL0"),
	}
	nodeClass.Spec.DataDisks = []v1alpha1.DataDiskSpec{
		{
			Category:         "cloud_essd",
			Size:             120,
			PerformanceLevel: lo.ToPtr("PL0"),
		},
	}
	return nodeClass
}

func (env *Environment) DefaultNodePool(nodeClass *v1alpha1.ECSNodeClass) *karpv1.NodePool {
	return &karpv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
		},
		Spec: karpv1.NodePoolSpec{
			Template: karpv1.NodeClaimTemplate{
				Spec: karpv1.NodeClaimTemplateSpec{
					TerminationGracePeriod: &metav1.Duration{Duration: 1 * time.Minute},
					ExpireAfter:            karpv1.MustParseNillableDuration("720h"),
					NodeClassRef: &karpv1.NodeClassReference{
						Group: object.GVK(nodeClass).Group,
						Kind:  object.GVK(nodeClass).Kind,
						Name:  nodeClass.Name,
					},
					Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      v1alpha1.LabelCapacityType,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{v1alpha1.CapacityTypeOnDemand},
							},
						},
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      corev1.LabelInstanceTypeStable,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"ecs.c6.xlarge", "ecs.g6.xlarge"},
							},
						},
						{
							NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"cn-hongkong-b", "cn-hongkong-c", "cn-hongkong-d"},
							},
						},
					},
				},
			},
		},
	}
}
