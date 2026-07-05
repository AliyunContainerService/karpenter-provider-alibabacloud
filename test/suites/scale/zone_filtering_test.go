//go:build integration

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

// Package scale contains integration tests that require a live ACK cluster.
//
// Required environment variables:
//
//	ALIBABA_CLOUD_ACCESS_KEY_ID
//	ALIBABA_CLOUD_ACCESS_KEY_SECRET
//	TEST_REGION            — e.g. cn-hangzhou
//	TEST_CLUSTER_ID        — ACK cluster ID
//	TEST_ZONE_A            — first zone ID,  e.g. cn-hangzhou-i (must have vSwitch in ECSNodeClass)
//	TEST_ZONE_B            — second zone ID, e.g. cn-hangzhou-j (must have vSwitch in ECSNodeClass)
//	TEST_VSWITCH_ZONE_A    — vSwitch ID in TEST_ZONE_A
//	TEST_VSWITCH_ZONE_B    — vSwitch ID in TEST_ZONE_B
//	TEST_SECURITY_GROUP_ID — security group ID for ECS instances
//
// Run with:
//
//	go test -v -tags integration ./test/suites/scale/... -run ZoneFiltering -timeout 30m
package scale

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// mustEnv returns the value of an environment variable or skips the test if it is unset.
func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		Skip(fmt.Sprintf("environment variable %s is required for this test", key))
	}
	return v
}

var _ = Describe("ZoneFiltering", Label("zone-filtering"), func() {
	var (
		ctx       context.Context
		k8sClient client.Client
		nodeClass *v1alpha1.ECSNodeClass
		nodePool  *karpv1.NodePool
		pod       *corev1.Pod
		zoneA     string
		zoneB     string
		vswitchA  string
		vswitchB  string
		sgID      string
	)

	BeforeEach(func() {
		ctx = context.Background()

		zoneA = mustEnv("TEST_ZONE_A")
		zoneB = mustEnv("TEST_ZONE_B")
		vswitchA = mustEnv("TEST_VSWITCH_ZONE_A")
		vswitchB = mustEnv("TEST_VSWITCH_ZONE_B")
		sgID = mustEnv("TEST_SECURITY_GROUP_ID")

		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = os.Getenv("HOME") + "/.kube/config"
		}
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		Expect(err).NotTo(HaveOccurred(), "failed to load kubeconfig")

		scheme := buildScheme()
		k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())

		nodeClass = &v1alpha1.ECSNodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "zone-filter-test-nc",
			},
			Spec: v1alpha1.ECSNodeClassSpec{
				VSwitchSelectorTerms: []v1alpha1.VSwitchSelectorTerm{
					{ID: &vswitchA},
					{ID: &vswitchB},
				},
				SecurityGroupSelectorTerms: []v1alpha1.SecurityGroupSelectorTerm{
					{ID: &sgID},
				},
				Tags: map[string]string{
					"testing/purpose": "zone-filter",
				},
			},
		}

		nodePool = &karpv1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: "zone-filter-test-pool"},
			Spec: karpv1.NodePoolSpec{
				Template: karpv1.NodeClaimTemplate{
					Spec: karpv1.NodeClaimTemplateSpec{
						NodeClassRef: &karpv1.NodeClassReference{
							Group: "karpenter.alibabacloud.com",
							Kind:  "ECSNodeClass",
							Name:  nodeClass.Name,
						},
						Requirements: []karpv1.NodeSelectorRequirementWithMinValues{
							{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      v1alpha1.LabelCapacityType,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{v1alpha1.CapacityTypeOnDemand},
							}},
							// Restrict to zone A only — zone B vswitch must NOT be selected.
							{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
								Key:      corev1.LabelTopologyZone,
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{zoneA},
							}},
						},
					},
				},
			},
		}

		pod = &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "zone-filter-test-pod",
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{
					karpv1.NodePoolLabelKey: nodePool.Name,
				},
				Containers: []corev1.Container{{
					Name:  "pause",
					Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				}},
			},
		}
	})

	AfterEach(func() {
		if k8sClient == nil {
			return
		}
		_ = k8sClient.Delete(ctx, pod)
		_ = k8sClient.Delete(ctx, nodePool)
		_ = k8sClient.Delete(ctx, nodeClass)
	})

	It("should provision node only in the zone required by the NodePool", func() {
		By("creating ECSNodeClass with vswitches in two zones")
		Expect(k8sClient.Create(ctx, nodeClass)).To(Succeed())

		By("creating NodePool restricted to zone A (" + zoneA + ")")
		Expect(k8sClient.Create(ctx, nodePool)).To(Succeed())

		By("creating pod to trigger scale-out")
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())

		By("waiting for pod to become Running")
		Eventually(func() corev1.PodPhase {
			p := &corev1.Pod{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), p); err != nil {
				return ""
			}
			return p.Status.Phase
		}, 20*time.Minute, 15*time.Second).Should(Equal(corev1.PodRunning))

		By("verifying the scheduled node is in zone A")
		p := &corev1.Pod{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), p)).To(Succeed())
		Expect(p.Spec.NodeName).NotTo(BeEmpty(), "pod should be scheduled to a node")

		node := &corev1.Node{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: p.Spec.NodeName}, node)).To(Succeed())

		actualZone := node.Labels[corev1.LabelTopologyZone]
		Expect(actualZone).To(Equal(zoneA),
			"node should be in zone %s (restricted by NodePool), got %s", zoneA, actualZone)
		Expect(actualZone).NotTo(Equal(zoneB),
			"node must NOT be in zone B — zone filtering is broken")
	})
})

func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = v1alpha1.AddToScheme(s)
	// karpenter v1 types (NodePool, NodeClaim) are registered via init() in karpv1 package;
	// mirror that registration here so the controller-runtime client can encode/decode them.
	karpGV := schema.GroupVersion{Group: "karpenter.sh", Version: "v1"}
	s.AddKnownTypes(karpGV,
		&karpv1.NodePool{},
		&karpv1.NodePoolList{},
		&karpv1.NodeClaim{},
		&karpv1.NodeClaimList{},
	)
	return s
}
