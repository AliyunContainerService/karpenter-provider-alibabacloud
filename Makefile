# Makefile for Karpenter Alibaba Cloud Provider

# Image URL to use all building/pushing image targets
REGISTRY ?= registry.cn-hangzhou.aliyuncs.com/acs
TAG ?= latest
IMG ?= $(REGISTRY)/karpenter-provider-alibabacloud:$(TAG)
TEST_SUITE ?= "..."
FOCUS ?=
SKIP ?=
UPSTREAM_TEST_SUITE ?= regression
E2E_GOFLAGS ?= -mod=mod
KARPENTER_CORE_DIR ?= $(shell GOFLAGS="$(E2E_GOFLAGS)" go list -m -f '{{ .Dir }}' sigs.k8s.io/karpenter)
DEFAULT_NODECLASS ?= $(shell pwd)/test/pkg/environment/alibabacloud/default_ecsnodeclass.yaml
DEFAULT_NODEPOOL ?= $(shell pwd)/test/pkg/environment/alibabacloud/default_nodepool.yaml
KWOK_NODECLASS ?= $(shell pwd)/test/pkg/environment/kwok/default_kwoknodeclass.yaml
KWOK_NODEPOOL ?= $(shell pwd)/test/pkg/environment/kwok/default_nodepool.yaml
KWOK_CLUSTER_NAME ?= karpenter-kwok-e2e
KWOK_LARGE_SCALE ?= true
KWOK_SCALE_REPLICAS ?= 1000
KWOK_REPORT_DIR ?= $(shell pwd)/.e2e/kwok
DEPLOY_CONFIG ?= deploy-config.yaml

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate CRD manifests
	$(CONTROLLER_GEN) crd:allowDangerousTypes=true paths="./pkg/apis/..." output:crd:artifacts:config=charts/karpenter/crds

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./pkg/apis/..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test-batcher
test-batcher: ## Run batcher tests only.
	go test -v ./pkg/batcher/... -coverprofile batcher-cover.out

##@ Build

# CGO configuration to avoid linker issues on macOS
CGO_ENABLED ?= 0

.PHONY: build
build: fmt vet ## Build manager binary.
	CGO_ENABLED=$(CGO_ENABLED) go build -o bin/controller cmd/controller/main.go

.PHONY: run
run: fmt vet ## Run a controller from your host.
	CGO_ENABLED=$(CGO_ENABLED) go run cmd/controller/main.go $(ARGS)

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

##@ Deployment

.PHONY: install
install: manifests ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	kubectl apply -f charts/karpenter/crds/

.PHONY: uninstall
uninstall: manifests ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	kubectl delete -f charts/karpenter/crds/

.PHONY: deploy
deploy: manifests ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd charts/karpenter && helm upgrade --install karpenter . --namespace karpenter --create-namespace

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	helm uninstall karpenter -n karpenter

##@ Build Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen

## Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.14.0

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

## Tool Binaries
ENVTEST ?= $(LOCALBIN)/setup-envtest

ENVTEST_VERSION ?= latest

# 安装/下载 envtest
envtest-setup: $(ENVTEST)
$(ENVTEST):
	mkdir -p $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || { \
		curl -Ss "https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/master/hack/setup-envtest.sh" | bash -s -- $(subst v,,$(ENVTEST_VERSION)); \
	}
	@echo "envtest installed"

# 获取 envtest 环境变量
ENVTEST_K8S_VERSION ?= 1.28.0

.PHONY: test
test: generate manifests envtest-setup fmt vet
	@KUBEBUILDER_ASSETS="$$($(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)"; \
	echo "KUBEBUILDER_ASSETS=$${KUBEBUILDER_ASSETS}"; \
	KUBEBUILDER_ASSETS="$${KUBEBUILDER_ASSETS}" go test -v ./pkg/... -coverprofile test.out

.PHONY: test-integration
test-integration: fmt vet ## Run integration tests in test/suites/...
	$(MAKE) e2etests

.PHONY: test-all
test-all: test test-integration ## Run all tests (unit + integration)

.PHONY: e2etests
e2etests: ## Run provider-specific e2e tests in test/suites/$TEST_SUITE against the current kubeconfig.
	cd test && GOFLAGS="$(E2E_GOFLAGS)" go test \
		-p 1 \
		-count 1 \
		-timeout 12h \
		-v \
		./suites/$(shell echo $(TEST_SUITE) | tr A-Z a-z)/... \
		--ginkgo.focus="$(FOCUS)" \
		--ginkgo.skip="$(SKIP)" \
		--ginkgo.timeout=3h20m \
		--ginkgo.grace-period=3m \
		--ginkgo.vv

.PHONY: e2e-parity
e2e-parity: ## Check AWS-provider E2E suite/workflow/action parity.
	GOFLAGS="$(E2E_GOFLAGS)" go run ./test/hack/e2e/parity --repo . --strict

.PHONY: e2e-discover-gpu
e2e-discover-gpu: ## Discover Alibaba Cloud GPU regions/zones for production e2e validation.
	GOFLAGS="$(E2E_GOFLAGS)" go run ./test/hack/e2e/ackctl discover-gpu --config "$(DEPLOY_CONFIG)" --format text

.PHONY: upstream-e2etests
upstream-e2etests: ## Run upstream Karpenter contract suites with AlibabaCloud default fixtures.
	cd $(KARPENTER_CORE_DIR) && \
		suite="$$(echo "$(UPSTREAM_TEST_SUITE)" | tr A-Z a-z)"; \
		focus="$(FOCUS)"; \
		if [[ "$${suite}" == "performance" && ! -d "./test/suites/performance" ]]; then \
			echo "upstream performance suite is not present in $(KARPENTER_CORE_DIR); pin a compatible suite or replacement contract" >&2; \
			exit 1; \
		fi; \
		GOFLAGS="$(E2E_GOFLAGS)" go test \
		-count 1 \
		-timeout 12h \
		-v \
		./test/suites/$${suite}/... \
		--ginkgo.focus="$${focus}" \
		--ginkgo.skip="$(SKIP)" \
		--ginkgo.timeout=3h \
		--ginkgo.grace-period=5m \
		--ginkgo.vv \
		--default-nodeclass="$(DEFAULT_NODECLASS)" \
		--default-nodepool="$(DEFAULT_NODEPOOL)"

.PHONY: kwok-core-e2e
kwok-core-e2e: ## Run upstream regression/performance contracts on kind + KWOK.
	KARPENTER_CORE_DIR="$(KARPENTER_CORE_DIR)" \
	KWOK_CLUSTER_NAME="$(KWOK_CLUSTER_NAME)" \
	KWOK_NODECLASS="$(KWOK_NODECLASS)" \
	KWOK_NODEPOOL="$(KWOK_NODEPOOL)" \
	UPSTREAM_TEST_SUITE="$(UPSTREAM_TEST_SUITE)" \
	FOCUS="$(FOCUS)" \
	SKIP="$(SKIP)" \
	E2E_GOFLAGS="$(E2E_GOFLAGS)" \
	KWOK_LARGE_SCALE="$(KWOK_LARGE_SCALE)" \
	KWOK_SCALE_REPLICAS="$(KWOK_SCALE_REPLICAS)" \
	KWOK_REPORT_DIR="$(KWOK_REPORT_DIR)" \
	./test/hack/kwok/run.sh
