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
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Metrics", func() {
	It("should expose Karpenter metrics from the controller pod", Label("metrics"), func() {
		configureNodeClassAndPool("metrics")
		env.ExpectCreated(nodeClass, nodePool)

		Eventually(func(g Gomega) {
			body := expectKarpenterMetricsBody(g)
			g.Expect(body).To(ContainSubstring("karpenter_"), "expected at least one karpenter_* metric")
		}, time.Minute, 5*time.Second).Should(Succeed())
	})
})

func expectKarpenterMetricsBody(g Gomega) string {
	GinkgoHelper()

	var pod *corev1.Pod
	for _, candidate := range env.ExpectKarpenterPods() {
		if candidate.Status.Phase == corev1.PodRunning {
			pod = candidate
			break
		}
	}
	g.Expect(pod).ToNot(BeNil(), "expected a running Karpenter pod")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	g.Expect(err).ToNot(HaveOccurred())
	localPort := listener.Addr().(*net.TCPAddr).Port
	g.Expect(listener.Close()).To(Succeed())

	ctx, cancel := context.WithCancel(env.Context)
	defer cancel()
	env.ExpectPodPortForwarded(ctx, pod, 8080, localPort)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", localPort))
	g.Expect(err).ToNot(HaveOccurred())
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	g.Expect(err).ToNot(HaveOccurred())
	return strings.TrimSpace(string(body))
}
