# AlibabaCloud Karpenter E2E Test Framework Design

**Date:** 2026-06-23

**Branch:** `feat/alibabacloud-test-framework`

**Status:** MVP implementation validated on a dedicated ACK cluster for the currently adapted suites; broader AWS-suite parity remains phased work.

## Goal

Build an AlibabaCloud provider E2E test framework by reusing the proven Karpenter AWS provider and upstream Karpenter test patterns instead of inventing a new test system.

The framework must support:

- Dedicated ACK cluster creation from AlibabaCloud credentials and cluster configuration.
- Provider-specific E2E suites for AlibabaCloud behavior.
- Upstream `sigs.k8s.io/karpenter` provider-neutral regression/performance contract tests.
- KWOK-based low-cost core behavior simulation.
- Scheduled soak and scale runs.
- Reliable cleanup of clusters and cloud resources created by tests.

No credential values from `<path-to-deploy-config>` are stored in this repository or written to logs.

## Current Repository Baseline

The current repository already contains a partial E2E foundation:

- `test/pkg/cs/environment.go` initializes AlibabaCloud CS/ECS/VPC/RAM clients from `TEST_REGION`, `ALIBABA_CLOUD_ACCESS_KEY_ID`, `ALIBABA_CLOUD_ACCESS_KEY_SECRET`, `TEST_CLUSTER_ID`, `TEST_CLUSTER_NAME`, and `TEST_CLUSTER_ENDPOINT`.
- `test/suites/scale` validates scale-up and scale-down behavior with `ECSNodeClass` and `NodePool`.
- `test/suites/nodeclaim` validates garbage collection when an ECS instance disappears outside Kubernetes.
- `go.mod` already depends on `github.com/aws/karpenter-provider-aws` and `sigs.k8s.io/karpenter`.
- `Makefile` currently has `test-integration`, but not AWS-style `e2etests` or `upstream-e2etests`.

The current `test/pkg/cs` package embeds AWS provider `common.Environment`. That is acceptable only as a short-term transition path for the existing tests. The target architecture must remove AWS test-package coupling from AlibabaCloud provider-specific tests. The first post-MVP milestone is to vendor or reimplement the minimal Kubernetes/Ginkgo/debug/monitor helper set in this repository and delete provider-specific imports from `github.com/aws/karpenter-provider-aws/test/...`.

## Architecture

### Provider-Specific E2E Layer

Provider-specific suites live under:

- `test/suites/scale`
- `test/suites/nodeclaim`
- Future suites: `drift`, `consolidation`, `scheduling`, `integration`, `storage`, `image`, and provider-specific interruption tests.

The AlibabaCloud environment layer lives under `test/pkg/cs` and is responsible for:

- Creating Kubernetes clients and Ginkgo helpers.
- Initializing CS/ECS/VPC/RAM clients.
- Providing `DefaultECSNodeClass()` and `DefaultNodePool()`.
- Cleaning Kubernetes objects owned by the test.
- Applying AlibabaCloud-specific expectations.

Longer term, common Kubernetes/Ginkgo/debug/monitor helpers should be owned in this repository, for example under `test/pkg/environment/common`, and must not import AWS API types or AWS provider test packages.

### Upstream Contract Test Layer

Upstream `sigs.k8s.io/karpenter/test/suites/{regression,performance}` is used as the provider-neutral contract layer.

This path must not use AWS provider `common.Environment`. It must use upstream Karpenter's unstructured fixture contract:

- `--default-nodeclass=<rendered ECSNodeClass YAML>`
- `--default-nodepool=<AlibabaCloud NodePool YAML>`

This contract is verified against `sigs.k8s.io/karpenter@v1.8.0`:

- `test/pkg/environment/common/environment.go:64` defines `flag.String("default-nodeclass", "", "Pass in a default cloud specific node class")`.
- `test/pkg/environment/common/environment.go:65` defines `flag.String("default-nodepool", "", "Pass in a default karpenter nodepool")`.
- `test/pkg/environment/common/environment.go:161` loads the default NodePool from the flag path when provided.
- `test/pkg/environment/common/environment.go:186-198` loads the default NodeClass YAML through `serializeryaml.NewDecodingSerializer` into `unstructured.Unstructured`.
- `test/README.md:116-117` shows the provider integration command passing both flags.

Required fixtures:

- `test/pkg/environment/alibabacloud/default_ecsnodeclass.yaml`
- `test/pkg/environment/alibabacloud/default_nodepool.yaml`

The NodeClass fixture must support variable substitution for:

- `TEST_CLUSTER_ID`
- `TEST_CLUSTER_NAME`
- `TEST_CLUSTER_ENDPOINT`
- image selector input
- security group discovery tags or IDs
- VSwitch discovery tags or IDs
- testing ownership tags

Fixture rendering uses Go `text/template` in `ackctl`, not shell `envsubst`. This gives explicit missing-key failures and avoids silent empty substitutions. `ackctl setup` writes rendered fixture paths into the emitted env file, and Makefile targets only consume those paths.

### ACK Setup and Cleanup Layer

Add a Go CLI under `test/hack/e2e/ackctl`. Composite workflow actions should call this CLI rather than encoding ACK orchestration directly in shell.

Commands:

- `ackctl setup --config <path> --suite <suite> --source <source> --cluster-name <name> --git-ref <sha> --output-env <path> --manifest <path>`
- `ackctl cleanup --manifest <path> --config <path>`
- `ackctl dump --manifest <path> --output-dir <path>`

The CLI reads local or CI-provided config matching the shape of `<path-to-deploy-config>`, with credentials supplied through environment variables or CI secrets where possible.

`ackctl` output contracts:

- `--output-env` is shell-source compatible `KEY=VALUE` format. Values are shell-escaped and never include raw AK/SK.
- `setup` exits `0` only when the cluster, controller, env file, and owner manifest are ready.
- `cleanup` exits `0` when all owned resources are deleted or confirmed absent. It exits non-zero when any owned resource remains after retries, but still records partial cleanup status in the manifest.
- `dump` exits `0` if diagnostic collection runs, even when some optional collectors fail; failed collectors are listed in the dump summary.
- `dump` collects Kubernetes events, pod descriptions, controller logs, Node/NodeClaim/NodePool/ECSNodeClass YAML, ACK cluster/nodepool status, ECS instance status, VPC/VSwitch/SecurityGroup state, and cleanup manifest state. Secret objects, kubeconfig tokens, and credential-like fields are redacted.

### Workflow Layer

Follow the AWS provider workflow shape, but replace AWS-specific actions with AlibabaCloud equivalents:

- `.github/workflows/e2e.yaml`
- `.github/workflows/e2e-soak-trigger.yaml`
- `.github/workflows/e2e-scale-trigger.yaml`
- `.github/actions/e2e/setup-cluster`
- `.github/actions/e2e/cleanup`
- `.github/actions/e2e/dump-logs`

`e2e.yaml` supports:

- `workflow_dispatch`
- `workflow_call`
- inputs: `git_ref`, `region`, `suite`, `source`, `k8s_version`, `cluster_name`, `cleanup`, `enable_metrics`
- `source`: `alibabacloud`, `upstream`, and `all`

MVP workflows should omit AWS OIDC, CodeBuild, ECR, SQS, and AWS-specific Slack actions. AlibabaCloud CI secrets provide AK/SK or RRSA-equivalent credentials.

## ACK Setup State Machine

`ackctl setup` is idempotent and records every owned cloud resource in a manifest.

1. Load config and credentials.
2. Redact sensitive fields before logging.
3. Resolve `cluster_name`, `git_ref`, region, suite, source, and run ID.
4. Validate prerequisites: ACK/CS/ECS/VPC/RAM permissions, region quotas, Kubernetes version, instance types, image selector, VSwitch/SecurityGroup input, addon configuration, CI network access to Go modules when `GOFLAGS=-mod=mod` is used, and local tools.
5. Create or validate VPC resources:
   - VPC
   - VSwitches
   - SecurityGroup
   - required tags
6. Create or validate RAM role and permissions needed by Karpenter-managed ECS nodes.
7. Create ACK cluster and system node pool.
8. Install or validate addons.
9. Wait for ACK cluster and system node pool readiness.
10. Write kubeconfig to a generated path.
11. Install CRDs and controller by Helm.
12. Configure autoscaling if enabled.
13. Emit an env file containing:
   - `KUBECONFIG`
   - `TEST_REGION`
   - `TEST_CLUSTER_ID`
   - `TEST_CLUSTER_NAME`
   - `TEST_CLUSTER_ENDPOINT`
14. Write the owner manifest for cleanup.

Each state has timeout, retry with backoff, and partial failure handling. ACK cluster creation has a default 45-minute timeout; node pool readiness has a default 30-minute timeout. Parallel scheduled workflows must respect configured ACK and ECS quotas. If setup fails and `cleanup=true`, `ackctl cleanup` runs using the manifest and ownership tags.

Cluster reuse is lease-based. A reused E2E cluster must have an owner manifest with a non-expired `leaseExpiresAt`, matching ownership tags, and matching cluster ID. Soak may reuse a leased cluster across runs; scale defaults to a fresh cluster unless the workflow explicitly passes a valid leased cluster name.

## Cleanup State Machine

`ackctl cleanup` only deletes resources owned by the current test run.

Deletion requires both:

- The resource is present in the owner manifest, and
- The resource carries required test ownership tags.

Steps:

1. Load manifest and config.
2. Delete test Kubernetes objects and Helm release.
3. Delete owned cloud resources in the dependency order defined below.
4. Recheck resources that do not support tags by exact manifest ID and parent ownership.
5. Poll for deletion and retry best effort.
6. Emit a cleanup report listing deleted, skipped, and failed resources.

Default behavior is `cleanup=true`. `cleanup=false` is allowed only for debugging and still requires ownership tags.

Cleanup runs in CI as an independent `if: always()` step. `best effort` means cleanup continues across independent resource groups to delete as much owned state as possible. It does not mean failures are hidden: any owned resource that remains after retries is recorded in the manifest and causes a non-zero cleanup exit.

Deletion uses this topology:

1. Kubernetes workload objects, NodePools, NodeClaims, ECSNodeClasses, and controller Helm release.
2. Karpenter-created ECS instances, then wait for instance release and ENI/disk detach.
3. Launch templates and unattached disks recorded in the manifest.
4. SLB resources: listeners, backend server groups, backend attachments, then load balancers.
5. Autoscaling configuration and ACK node pools.
6. ACK cluster.
7. SecurityGroup rules and SecurityGroups, after ENIs are gone.
8. NAT/SNAT/route table artifacts if the setup created them.
9. VSwitches.
10. VPC.
11. RAM bindings: detach policies, remove trust bindings, then delete roles or policies owned by the run.

Each manifest resource has a cleanup state: `pending`, `deleted`, `skipped`, or `failed`. Re-running cleanup resumes from the manifest and retries only `pending` and `failed` resources, after rechecking whether previously failed resources are already absent.

## Owner Manifest

`ackctl setup` writes an owner manifest to a generated artifact path such as `.e2e/<cluster-name>/owner-manifest.yaml` for local runs, or `$RUNNER_TEMP/ack-e2e/owner-manifest.yaml` in CI.

The manifest records:

- `clusterName`
- `clusterID`
- `region`
- `gitRef`
- `suite`
- `source`
- `createdAt`
- `kubeconfigPath`
- required ownership tags
- resources grouped by type, ID, name, and tag support

Resources that support AlibabaCloud tags are cleaned by manifest plus tag verification. Resources that do not support tags are cleaned only when the manifest records their exact ID and they are transitively owned by the test cluster.

If `cluster_name` is provided to reuse a cluster, setup must verify all of these before running tests:

- The ACK cluster exists in the requested region.
- The cluster has the required test ownership tag or matches an owner manifest from a previous E2E setup.
- The kubeconfig endpoint and cluster ID match the resolved cluster.
- The system node pool is dedicated to the E2E run.

If any check fails, setup must refuse to run rather than treating an arbitrary existing cluster as test-owned.

## Tagging Contract

Every cloud resource created for E2E must carry:

- `testing/type=e2e`
- `testing/cluster=<cluster-name>`
- `karpenter.sh/discovery=<cluster-name>`
- `test/git_ref=<git-sha>`

Optional tags:

- CI run URL
- suite name
- source mode

Before relying on tag-based cleanup, the controller instance creation path must merge tags correctly. In `createInstanceWithRetry`, `nodeClass.Spec.Tags` must not replace Karpenter management tags. The required behavior is:

- Management tags are generated first.
- User tags from `ECSNodeClass.Spec.Tags` are merged afterward.
- User tags cannot override protected provider keys.
- E2E-only tags such as `testing/type`, `testing/cluster`, and `test/git_ref` are supplied by test fixtures or test suites, not by production controller defaults.

This is a blocking implementation prerequisite for safe cleanup.

Protected tag keys for the current API constants are:

- `karpenter.sh/managed-by` (`v1alpha1.TagManagedBy`)
- `karpenter.sh/cluster-id` (`v1alpha1.TagClusterID`)
- `karpenter.sh/discovery` (`v1alpha1.TagDiscovery`)
- `karpenter.sh/nodepool` (`v1alpha1.TagNodePool`)
- `karpenter.sh/nodeclaim` (`v1alpha1.TagNodeClaim`)
- `kubernetes.io/cluster` (`v1alpha1.TagCluster`)

The merge implementation belongs in `buildInstanceTags` and the immediate call path in `createInstanceWithRetry`, before invoking the instance provider. The instance provider receives a fully merged tag map and must not reinterpret ownership. Merge rule:

1. Start with protected management tags.
2. Add non-protected NodeClaim-derived tags.
3. Add non-protected `ECSNodeClass.Spec.Tags`.
4. Reject or ignore any user-provided key that matches a protected key.

`DefaultImageID` in `test/pkg/cs/environment.go` is also a portability risk. Provider-specific defaults must be replaced by config-driven image selector input, for example `TEST_IMAGE_ID`, `TEST_IMAGE_FAMILY`, or rendered NodeClass fixture fields. The same image selection source should feed both `test/pkg/cs` and upstream fixture rendering.

## Test Entrypoints

Add Makefile targets mirroring AWS provider ergonomics.

Provider-specific tests:

```bash
TEST_SUITE=scale make e2etests
TEST_SUITE=nodeclaim make e2etests
FOCUS="Scale Up" TEST_SUITE=scale make e2etests
SKIP="spot" TEST_SUITE=scale make e2etests
```

Target shape:

```bash
cd test && go test \
  -p 1 \
  -count 1 \
  -timeout 12h \
  -v \
  ./suites/${TEST_SUITE_LOWER}/... \
  --ginkgo.focus="${FOCUS}" \
  --ginkgo.skip="${SKIP}" \
  --ginkgo.timeout=3h20m \
  --ginkgo.grace-period=3m \
  --ginkgo.vv
```

Suite concurrency is handled at the workflow matrix layer, not inside a single
ACK cluster. `E2EMatrix` runs each provider suite in its own dedicated cluster
and exposes `max_parallel` to control how many suite clusters may run at the same
time. This is the safe parallelization boundary because the suite environment
uses cluster-wide cleanup, bootstrap-node tainting, and global Node/NodeClaim
count assertions.

Do not raise `go test -p` or enable Ginkgo process-level parallelism for ACK
provider suites until the suites are made fully namespace/resource isolated.
Running specs concurrently in one cluster can cause one spec's `BeforeEach` or
`AfterEach` cleanup to delete another spec's NodePool, ECSNodeClass, Pod, PV, or
NodeClaim, and global count assertions such as `EventuallyExpectCreatedNodeCount`
will become nondeterministic.

Upstream contract tests:

```bash
UPSTREAM_TEST_SUITE=regression make upstream-e2etests
UPSTREAM_TEST_SUITE=performance make upstream-e2etests
```

MVP enables `regression` first. `performance` is wired but can be gated until scale and cost limits are explicit.

The default MVP upstream command should start with a small, explicit focus list that avoids known provider gaps:

```bash
UPSTREAM_TEST_SUITE=regression \
FOCUS="DaemonSet|NodeClaim|Expiration|StaticCapacity" \
SKIP="Disruption|Interruption|IPv6|Windows|GPU" \
make upstream-e2etests
```

The focus/skip list is not permanent coverage. It is the bring-up gate used until the AlibabaCloud suite gap matrix is implemented.

The MVP focus tokens are verified against `sigs.k8s.io/karpenter@v1.8.0`:

- `DaemonSet`: `test/suites/regression/integration_test.go:40`
- `NodeClaim`: `test/suites/regression/nodeclaim_test.go:41`
- `Expiration`: `test/suites/regression/expiration_test.go:35`
- `StaticCapacity`: `test/suites/regression/staticcapacity_test.go:35`

KWOK:

```bash
make kwok-core-e2e
```

KWOK uses kind/KWOK and upstream regression/performance suites. It does not validate AlibabaCloud ECS/VPC/RAM/ACK API behavior.

`source` in `e2e.yaml` means suite source: `alibabacloud`, `upstream`, or `all`. KWOK is a cluster substrate, so it should be a separate workflow or target rather than overloading `source` in the ACK workflow. `kwok-core-e2e` creates kind/KWOK, deploys the upstream KWOK controller, and then runs the same upstream suite command with KWOK default fixtures.

The implementation plan must decide one dependency strategy:

- Regenerate and commit `vendor/` with `go mod vendor`, or
- Make E2E targets explicitly use `GOFLAGS=-mod=mod`.

MVP chooses `GOFLAGS=-mod=mod` for E2E-only commands to avoid blocking framework bring-up on vendor churn. A later cleanup can regenerate `vendor/` once dependencies stabilize.

If CI cannot reach the configured Go proxy or upstream repositories, this choice must be reversed before enabling CI: regenerate and commit `vendor/` and run E2E commands without network module resolution.

## Suite Mapping

| AWS suite or mode | AlibabaCloud mapping | MVP status |
| --- | --- | --- |
| `Scale` | Existing `test/suites/scale`, adapted around `ECSNodeClass` and ACK | Implemented in MVP |
| `NodeClaim` | Existing `test/suites/nodeclaim` garbage collection | Implemented in MVP |
| `Consolidation` | Provider-neutral disruption behavior plus AlibabaCloud node lifecycle expectations | Phase 1 |
| `Drift` | ECSNodeClass hash, image, VSwitch, SecurityGroup, and NodePool mutation drift | Phase 1 |
| `Scheduling` | Provider-neutral scheduling constraints with AlibabaCloud labels/instance types | Phase 1 |
| `Integration` | Split AWS-specific cases from provider-neutral cases; rewrite ECS/VPC/RAM-specific assertions | Phase 1/2 |
| `Storage` | ACK CSI and ESSD cloud disk semantics | Phase 2 |
| `AMI` | Rename to `Image`; validate `ImageSelectorTerms`, image family, owner, ID/name selection | Phase 2 |
| `Interruption` | Requires AlibabaCloud interruption/preemption event source design | Provider gap until designed |
| `IPv6` | ACK IPv6 support matrix driven | Provider gap until confirmed |
| `LocalZone` | ACK zone/product capability driven | Provider gap until confirmed |
| `PrivateCluster` | ACK private cluster topology and CI reachability design | Provider gap until confirmed |
| `Soak` | Workflow mode, not a separate suite; runs a stable subset repeatedly | Phase 2 |
| Upstream `regression` | Provider-neutral contract through default ECSNodeClass/NodePool fixtures | MVP partial |
| Upstream `performance` | Provider-neutral scale/perf contract; ACK costs must be bounded | Phase 2 |
| KWOK | Core simulation via upstream KWOK provider | MVP as separate fast gate |

Suites in provider-gap status must be skipped explicitly with reason. They must not be reported as passing AlibabaCloud E2E coverage.

## Dedicated Cluster Only in MVP

The current environment layer taints existing nodes and expects a clean `default` namespace. That behavior is unsafe for shared clusters.

MVP supports only dedicated ACK test clusters. Existing cluster mode is allowed only when the cluster was created for E2E and carries required ownership tags.

Shared existing cluster support requires a later design with:

- Namespace isolation.
- Node ownership detection.
- No global node tainting.
- No default namespace cleanliness requirement.
- Cleanup limited to test-owned objects.

## KWOK Strategy

KWOK is a separate gate named `kwok-core-e2e`.

It validates:

- Upstream regression behavior.
- Upstream performance scenarios where real cloud cost would be high.
- Scheduler/disruption/control-plane behavior at large scale.

It does not validate:

- ACK cluster creation.
- ECS instance lifecycle.
- VPC/VSwitch/SecurityGroup resolution.
- RAM role behavior.
- AlibabaCloud image and disk semantics.
- Provider-specific interruption handling.

## MVP Implementation Boundary

The first implementation milestone is intentionally narrow:

1. Add `e2etests` for `scale` and `nodeclaim`.
2. Add upstream default `ECSNodeClass` and `NodePool` YAML fixtures.
3. Add `upstream-e2etests` for `regression` with focus/skip support.
4. Add `ackctl setup`, `cleanup`, and `dump` skeleton with real config parsing, redaction, env emission, and manifest handling.
5. Add ACK setup/cleanup workflow actions that call `ackctl`.
6. Add `e2e.yaml` workflow for manual and reusable E2E execution.
7. Add `e2e-soak-trigger.yaml` and `e2e-scale-trigger.yaml`.
8. Fix protected tag merging before enabling cloud-resource cleanup by tag.
9. Add `kwok-core-e2e` as a separate local/CI target.

## Validation Plan

Design-time validation:

```bash
git status --short --branch
GOFLAGS=-mod=mod go test -run '^$' ./test/...
```

MVP implementation validation:

```bash
make fmt
make vet
GOFLAGS=-mod=mod TEST_SUITE=scale go test -run '^$' ./test/suites/scale/...
GOFLAGS=-mod=mod TEST_SUITE=nodeclaim go test -run '^$' ./test/suites/nodeclaim/...
```

ACK dry-run validation:

```bash
go run ./test/hack/e2e/ackctl setup --config <path-to-deploy-config> --dry-run
```

Real ACK validation:

```bash
go run ./test/hack/e2e/ackctl setup --config <path-to-deploy-config> --output-env /tmp/ack-e2e.env --manifest /tmp/ack-e2e-manifest.yaml
source /tmp/ack-e2e.env
TEST_SUITE=scale make e2etests
go run ./test/hack/e2e/ackctl cleanup --manifest /tmp/ack-e2e-manifest.yaml --config <path-to-deploy-config>
```

KWOK validation:

```bash
make kwok-core-e2e
```

## Required Environment

Local and CI:

- Go 1.24.x
- `kubectl`
- `helm`
- `ginkgo`
- `yq`
- AlibabaCloud credentials with ACK/CS/ECS/VPC/RAM permissions
- Region quota for ACK clusters, ECS instances, disks, VPC/VSwitch/SecurityGroup resources
- Region-valid image selector and instance types
- CI network access to the configured Go proxy when `GOFLAGS=-mod=mod` is used

KWOK only:

- Docker
- kind
- ko

## Open Risks

- AlibabaCloud interruption/preemption event source is not mapped yet.
- ACK IPv6, private cluster, and local-zone equivalents need a supported capability matrix.
- Upstream `performance` can create meaningful cost and quota pressure on real ACK clusters; default CI should not run it against ACK until limits are explicit.
- Current vendoring state requires either CI network access for explicit `GOFLAGS=-mod=mod` E2E commands or vendor regeneration before CI enablement.
- Existing `test/pkg/cs` cleanup and taint behavior is safe only for dedicated clusters. `test/pkg/cs.Environment.BeforeEach` must check E2E cluster ownership before tainting nodes or cleaning objects.

## Review Record

- Architecture review round 1: rejected. Blocking issues were AWS common coupling, incomplete Makefile targets, missing ACK state machine, unsafe tag override, existing-cluster risk, missing suite gap matrix, and unclear KWOK boundary.
- Architecture review round 2: passed for the design, with follow-up requirements for manifest-first cleanup safety, explicit upstream focus/skip, cluster reuse ownership checks, vendor strategy, and cleanup topology.
- Implementation architecture review: rejected until the MVP fixed manifest persistence after ACK creation, cleanup fallback removal, final deletion polling, and `DefaultECSNodeClass().Spec.ClusterName`.
- Claude implementation review: rejected until the MVP removed production-path `testing/*` tag defaults, added destructive E2E cluster guards, ignored local `.e2e` artifacts, and fixed Helm env passthrough for custom `valueFrom`.
- Current status: the blocking implementation review items above have been addressed in code and covered by local tests. Remaining gaps are tracked as phase work: workflow files, KWOK target, full diagnostic dump, all AWS-suite equivalents, and upstream regression/performance expansion.
