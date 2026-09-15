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

package integration

import (
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ECSNodeClass Hash", func() {
	It("should have ECSNodeClass hash annotations", Label("hash"), func() {
		configureNodeClassAndPool("hash")
		env.ExpectCreated(nodeClass)

		Eventually(func(g Gomega) {
			updated := &v1alpha1.ECSNodeClass{}
			g.Expect(env.Client.Get(env.Context, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			g.Expect(updated.Annotations).To(HaveKey(v1alpha1.AnnotationECSNodeClassHash))
			g.Expect(updated.Annotations).To(HaveKey(v1alpha1.AnnotationECSNodeClassHashVersion))
			g.Expect(updated.Annotations[v1alpha1.AnnotationECSNodeClassHash]).ToNot(BeEmpty())
		}).Should(Succeed())
	})
})
