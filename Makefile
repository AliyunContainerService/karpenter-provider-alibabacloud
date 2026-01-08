# Makefile for Karpenter Alibaba Cloud Provider

# Image URL to use all building/pushing image targets
IMG ?= registry.cn-hangzhou.aliyuncs.com/acs/karpenter-provider-alibabacloud:latest

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
	ginkgo -v --procs=1 -timeout 2h ./test/suites/...

.PHONY: test-all
test-all: test test-integration ## Run all tests (unit + integration)
