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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aws/karpenter-provider-aws/test/pkg/debug"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

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
	"k8s.io/client-go/util/retry"
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

var DefaultImageFamily = "acs:alibaba_cloud_linux_3_2104_x64_container_optimized"

var DefaultImageID = "aliyun_4_x64_20G_container_optimized_alibase_20260430.vhd"

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

func (env *Environment) ExpectUpdated(objects ...client.Object) {
	GinkgoHelper()
	for _, object := range objects {
		if nodeClass, ok := object.(*v1alpha1.ECSNodeClass); ok {
			env.expectECSNodeClassPatched(nodeClass)
			continue
		}
		env.Environment.ExpectUpdated(object)
	}
}

func (env *Environment) expectECSNodeClassPatched(nodeClass *v1alpha1.ECSNodeClass) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		current := &v1alpha1.ECSNodeClass{}
		g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), current)).To(Succeed())
		base := current.DeepCopy()
		if nodeClass.Labels != nil {
			current.Labels = nodeClass.Labels
		}
		if nodeClass.Annotations != nil {
			current.Annotations = nodeClass.Annotations
		}
		current.Spec = nodeClass.Spec
		g.Expect(env.Client.Patch(env.Context, current, client.MergeFrom(base))).To(Succeed())
	}).WithTimeout(30 * time.Second).Should(Succeed())
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
	cfg := lo.Must(loadTestConfig())

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

func loadTestConfig() (TestConfig, error) {
	cfg := TestConfig{
		Region:          strings.TrimSpace(os.Getenv("TEST_REGION")),
		AccessKeyID:     strings.TrimSpace(os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID")),
		AccessKeySecret: strings.TrimSpace(os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET")),
	}
	if cfg.Region != "" && cfg.AccessKeyID != "" && cfg.AccessKeySecret != "" {
		return cfg, nil
	}
	if path := strings.TrimSpace(os.Getenv("TEST_DEPLOY_CONFIG")); path != "" {
		fileCfg, err := loadDeployConfigTestCredentials(path)
		if err != nil {
			return TestConfig{}, err
		}
		if cfg.Region == "" {
			cfg.Region = fileCfg.Region
		}
		if cfg.AccessKeyID == "" {
			cfg.AccessKeyID = fileCfg.AccessKeyID
		}
		if cfg.AccessKeySecret == "" {
			cfg.AccessKeySecret = fileCfg.AccessKeySecret
		}
	}
	if cfg.Region == "" {
		return TestConfig{}, fmt.Errorf("TEST_REGION is required")
	}
	if cfg.AccessKeyID == "" {
		return TestConfig{}, fmt.Errorf("ALIBABA_CLOUD_ACCESS_KEY_ID or TEST_DEPLOY_CONFIG is required")
	}
	if cfg.AccessKeySecret == "" {
		return TestConfig{}, fmt.Errorf("ALIBABA_CLOUD_ACCESS_KEY_SECRET or TEST_DEPLOY_CONFIG is required")
	}
	return cfg, nil
}

func loadDeployConfigTestCredentials(path string) (TestConfig, error) {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return TestConfig{}, fmt.Errorf("read TEST_DEPLOY_CONFIG: %w", err)
	}
	var raw struct {
		AlibabaCloud struct {
			RegionID        string `yaml:"region_id"`
			AccessKeyID     string `yaml:"access_key_id"`
			AccessKeySecret string `yaml:"access_key_secret"`
		} `yaml:"alibaba_cloud"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return TestConfig{}, fmt.Errorf("parse TEST_DEPLOY_CONFIG: %w", err)
	}
	return TestConfig{
		Region:          strings.TrimSpace(raw.AlibabaCloud.RegionID),
		AccessKeyID:     strings.TrimSpace(raw.AlibabaCloud.AccessKeyID),
		AccessKeySecret: strings.TrimSpace(raw.AlibabaCloud.AccessKeySecret),
	}, nil
}

func (env *Environment) BeforeEach() {
	Expect(validateTestClusterTarget(env.ClusterID, env.ClusterName)).To(Succeed())

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

	// Delete leftover Deployments in the default namespace so their pods don't linger
	// and fail ValidateCleanEnvironment's "no pods in default namespace" check.
	deployList := &appsv1.DeploymentList{}
	if err := env.Client.List(context.Background(), deployList, client.InNamespace("default")); err == nil {
		for i := range deployList.Items {
			_ = env.Client.Delete(context.Background(), &deployList.Items[i], &client.DeleteOptions{})
		}
	}

	// Wait for all karpenter-managed nodes to fully terminate before capturing
	// StartingNodeCount. A node still terminating from the previous test would inflate
	// StartingNodeCount, making CreatedNodeCount return 0 for newly provisioned nodes
	// (e.g. gc_stability_test.go:136, deprovisioning_test.go:246).
	Eventually(func(g Gomega) {
		nl := &corev1.NodeList{}
		g.Expect(env.Client.List(context.Background(), nl)).To(Succeed())
		karpenterNodes := lo.Filter(nl.Items, func(n corev1.Node, _ int) bool {
			_, hasLabel := n.Labels[karpv1.NodePoolLabelKey]
			return hasLabel
		})
		g.Expect(karpenterNodes).To(BeEmpty(), "waiting for all karpenter-managed nodes to terminate before next test")
	}).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

	// Wait for all pods in the default namespace to be deleted.
	// ValidateCleanEnvironment rejects any pod in the default namespace.
	Eventually(func(g Gomega) {
		podList := &corev1.PodList{}
		g.Expect(env.Client.List(context.Background(), podList, client.InNamespace("default"))).To(Succeed())
		// Explicitly delete any remaining pods to speed up termination
		for i := range podList.Items {
			_ = env.Client.Delete(context.Background(), &podList.Items[i], &client.DeleteOptions{})
		}
		g.Expect(podList.Items).To(BeEmpty(), "waiting for pods in default namespace to terminate")
	}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

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
			Expect(env.ensureBootstrapNodeTaint(node.Name)).To(Succeed())
		}
	}

	// Expect this cluster to be clean for test runs to execute successfully
	env.ValidateCleanEnvironment()

	env.Monitor.Reset()
	env.StartingNodeCount = env.Monitor.NodeCountAtReset()
}

func (env *Environment) ensureBootstrapNodeTaint(name string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		node := &corev1.Node{}
		if err := env.Client.Get(context.Background(), types.NamespacedName{Name: name}, node); err != nil {
			return client.IgnoreNotFound(err)
		}
		for _, taint := range node.Spec.Taints {
			if taint.Key == "karpenter-test" {
				return nil
			}
		}
		node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{
			Key:    "karpenter-test",
			Value:  "true",
			Effect: corev1.TaintEffectNoSchedule,
		})
		return env.Client.Update(context.Background(), node, &client.UpdateOptions{})
	})
}

func (env *Environment) AfterEach() {
	env.CleanupObjects(CleanableObjects...)
	debug.AfterEach(env.Context)
}

func (env *Environment) ValidateCleanEnvironment() {
	Eventually(func(g Gomega) {
		var nodes corev1.NodeList
		g.Expect(env.Client.List(env.Context, &nodes)).To(Succeed())
		for _, node := range nodes.Items {
			if node.Labels[karpv1.NodePoolLabelKey] != "" && len(node.Spec.Taints) == 0 && !node.Spec.Unschedulable {
				g.Expect(node.Name).To(BeEmpty(), fmt.Sprintf("expected system pool node %s to be tainted", node.Name))
			}
		}
		var pods corev1.PodList
		g.Expect(env.Client.List(env.Context, &pods)).To(Succeed())
		for i := range pods.Items {
			g.Expect(pod.IsProvisionable(&pods.Items[i])).To(BeFalse(),
				fmt.Sprintf("expected to have no provisionable pods, found %s/%s", pods.Items[i].Namespace, pods.Items[i].Name))
			g.Expect(pods.Items[i].Namespace).ToNot(Equal("default"),
				fmt.Sprintf("expected no pods in the `default` namespace, found %s/%s", pods.Items[i].Namespace, pods.Items[i].Name))
		}
		for _, obj := range []client.Object{&karpv1.NodePool{}, &v1alpha1.ECSNodeClass{}} {
			metaList := &metav1.PartialObjectMetadataList{}
			gvk := lo.Must(apiutil.GVKForObject(obj, env.Client.Scheme()))
			metaList.SetGroupVersionKind(gvk)
			g.Expect(env.Client.List(env.Context, metaList, client.Limit(1))).To(Succeed())
			g.Expect(metaList.Items).To(HaveLen(0), fmt.Sprintf("expected no %s to exist", gvk.Kind))
		}
	}).WithTimeout(10 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
}

func (env *Environment) DefaultECSNodeClass() *v1alpha1.ECSNodeClass {
	nodeClass := &v1alpha1.ECSNodeClass{}

	nodeClass.ObjectMeta = test.ObjectMeta(nodeClass.ObjectMeta)

	nodeClass.Spec.ClusterID = env.ClusterID
	nodeClass.Spec.ClusterName = env.ClusterName
	nodeClass.Spec.Tags = env.OwnershipTags()
	nodeClass.Spec.SecurityGroupSelectorTerms = securityGroupSelectorTerms(env.ClusterName)
	nodeClass.Spec.VSwitchSelectorTerms = vSwitchSelectorTerms(env.ClusterName)
	nodeClass.Spec.ImageSelectorTerms = imageSelectorTerms()
	if role := strings.TrimSpace(os.Getenv("TEST_RAM_ROLE")); role != "" {
		nodeClass.Spec.Role = lo.ToPtr(role)
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

func (env *Environment) OwnershipTags() map[string]string {
	return map[string]string{
		"testing/cluster":        env.ClusterName,
		"karpenter.sh/discovery": env.ClusterName,
		v1alpha1.TagManagedBy:    v1alpha1.TagManagedByValue,
	}
}

func (env *Environment) TestTags(testType string) map[string]string {
	tags := env.OwnershipTags()
	tags["testing/type"] = testType
	return tags
}

func validateTestClusterTarget(clusterID, clusterName string) error {
	if strings.TrimSpace(clusterID) == "" {
		return fmt.Errorf("TEST_CLUSTER_ID is required before mutating an E2E cluster")
	}
	if strings.TrimSpace(clusterName) == "" {
		return fmt.Errorf("TEST_CLUSTER_NAME is required before mutating an E2E cluster")
	}
	if strings.HasPrefix(clusterName, "karpenter-alibabacloud-e2e-") || os.Getenv("ALLOW_UNSAFE_E2E_CLUSTER") == "true" {
		return nil
	}
	return fmt.Errorf("refusing to run destructive E2E setup against non-e2e cluster %q", clusterName)
}

func vSwitchSelectorTerms(clusterName string) []v1alpha1.VSwitchSelectorTerm {
	if ids := splitEnvList("TEST_VSWITCH_IDS"); len(ids) > 0 {
		return lo.Map(ids, func(id string, _ int) v1alpha1.VSwitchSelectorTerm {
			return v1alpha1.VSwitchSelectorTerm{ID: lo.ToPtr(id)}
		})
	}
	return []v1alpha1.VSwitchSelectorTerm{
		{
			Tags: map[string]string{"karpenter.sh/discovery": clusterName},
		},
	}
}

func securityGroupSelectorTerms(clusterName string) []v1alpha1.SecurityGroupSelectorTerm {
	if ids := splitEnvList("TEST_SECURITY_GROUP_IDS"); len(ids) > 0 {
		return lo.Map(ids, func(id string, _ int) v1alpha1.SecurityGroupSelectorTerm {
			return v1alpha1.SecurityGroupSelectorTerm{ID: lo.ToPtr(id)}
		})
	}
	return []v1alpha1.SecurityGroupSelectorTerm{
		{
			Tags: map[string]string{"karpenter.sh/discovery": clusterName},
		},
	}
}

func imageSelectorTerms() []v1alpha1.ImageSelectorTerm {
	if imageID := strings.TrimSpace(os.Getenv("TEST_IMAGE_ID")); imageID != "" {
		return []v1alpha1.ImageSelectorTerm{{ID: lo.ToPtr(imageID)}}
	}
	imageFamily := strings.TrimSpace(os.Getenv("TEST_IMAGE_FAMILY"))
	if imageFamily == "" {
		imageFamily = DefaultImageFamily
	}
	return []v1alpha1.ImageSelectorTerm{{ImageFamily: lo.ToPtr(imageFamily)}}
}

func splitEnvList(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func (env *Environment) DefaultNodePool(nodeClass *v1alpha1.ECSNodeClass) *karpv1.NodePool {
	return &karpv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
		},
		Spec: karpv1.NodePoolSpec{
			Template: karpv1.NodeClaimTemplate{
				// Propagate the discovery label to nodes so ConsistentlyExpectNodeCount
				// (which filters by test.DiscoveryLabel = "testing/cluster") can find them.
				ObjectMeta: karpv1.ObjectMeta{
					Labels: map[string]string{
						test.DiscoveryLabel: "unspecified",
					},
				},
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
								Values:   []string{"ecs.c9i.large", "ecs.c9i.xlarge"},
							},
						},
					},
				},
			},
		},
	}
}
