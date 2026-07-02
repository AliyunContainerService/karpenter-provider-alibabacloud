# Issue #8: Over-Provisioning Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix four root causes that together cause Karpenter to spawn too many ECS instances: missing `Offering.Requirements`, broken GC due to tag mismatch, all zones marked available regardless of real stock, and no spec.limits in example NodePool.

**Architecture:** Two small independent fixes (TagManagedBy, spec.limits) + one integrated inventory fix: add `DescribeAvailableResource` to `ECSClient`, extend `ZoneInfo.ChargeTypes` in `instancetype.Provider`, and populate `Offering.Requirements` in `cloudprovider.GetInstanceTypes()` with real zone+chargeType data.

**Tech Stack:** `github.com/alibabacloud-go/ecs-20140526/v5/client` (already in go.mod), `sigs.k8s.io/karpenter/pkg/scheduling`, `sigs.k8s.io/karpenter/pkg/apis/v1`.

**Branch:** `fix/issue-8-overprovisioning` from `origin/opensource-main`

## Global Constraints

- Do not change the `ECSClient` interface shape beyond adding `DescribeAvailableResource`. All existing mock implementations must be updated to satisfy the interface (add a stub returning `nil, errors.New("not implemented")` if unused).
- Inventory call failures are non-fatal: fall back to optimistic availability (all charge types assumed available) so the provider degrades gracefully.
- `scheduling.NewRequirements` / `scheduling.NewRequirement` come from `sigs.k8s.io/karpenter/pkg/scheduling` — add this import to `cloudprovider.go`.
- Karpenter capacity type strings: `"on-demand"` = `coreapis.CapacityTypeOnDemand`, `"spot"` = `coreapis.CapacityTypeSpot`.
- ECS charge type strings: `"PostPaid"` (on-demand), `"SpotAsPriceGo"` (spot).
- Stock status values from ECS: `"WithStock"` and `"ClosedWithStock"` = has stock; `"WithoutStock"` and `"ClosedWithoutStock"` = no stock.

---

## File Map

| File | Change |
|------|--------|
| `examples/default-nodepool.yaml` | Add `spec.limits` section |
| `pkg/cloudprovider/cloudprovider.go` | Fix TagManagedBy ("true"→"karpenter"); populate Offering.Requirements; add `scheduling` import |
| `pkg/clients/ecs.go` | Add `DescribeAvailableResource` to `ECSClient` interface + implement in `DefaultECSClient` |
| `pkg/providers/instancetype/instancetype.go` | Extend `ZoneInfo` with `ChargeTypes []string`; add `getInventory()` cache method; use inventory in `List()` per-instance-type zone building |
| `pkg/providers/instance/instance_test.go` | Add `DescribeAvailableResource` stub to `MockECSClient` |

---

## Task 1: Add `spec.limits` to Example NodePool

**Files:**
- Modify: `examples/default-nodepool.yaml`

Quick fix — adds a conservative default limit to prevent unbounded provisioning.

- [ ] **Step 1: Add limits to NodePool YAML**

Edit `examples/default-nodepool.yaml`. Add `spec.limits` section (place it at the top level of `spec`, before `spec.template`):

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: default
spec:
  limits:
    cpu: "100"       # 100 vCPU total across this NodePool
    memory: 400Gi    # 400 GiB total memory
  template:
    spec:
      terminationGracePeriod: 1m
      expireAfter: 720h
      nodeClassRef:
        group: karpenter.alibabacloud.com
        kind: ECSNodeClass
        name: default
      requirements:
      - key: "karpenter.sh/capacity-type"
        operator: In
        values: ["on-demand"]
      - key: "node.kubernetes.io/instance-type"
        operator: In
        values: ["ecs.c6.xlarge", "ecs.g6.xlarge"]
      - key: "topology.kubernetes.io/zone"
        operator: In
        values: ["cn-hongkong-b", "cn-hongkong-c", "cn-hongkong-d"]
```

- [ ] **Step 2: Commit**

```bash
git add examples/default-nodepool.yaml
git commit -m "fix: add spec.limits to example NodePool to prevent unbounded provisioning"
```

---

## Task 2: Fix `TagManagedBy` Value Mismatch

**Files:**
- Modify: `pkg/cloudprovider/cloudprovider.go` (~line 604)

`buildInstanceTags` sets `"true"` but `List()` queries `"karpenter"`. The ECS tag filter is exact-match, so `List()` always returns empty → GC deletes NodeClaims → re-provisioning loop.

- [ ] **Step 1: Fix the tag value**

In `pkg/cloudprovider/cloudprovider.go`, find `buildInstanceTags` function and change:

```go
// Before:
tags[v1alpha1.TagManagedBy] = "true"

// After:
tags[v1alpha1.TagManagedBy] = "karpenter"
```

The `List()` method at ~line 295 already filters for `"karpenter"`:
```go
tags := map[string]string{
    v1alpha1.TagManagedBy: "karpenter",  // This is already correct
}
```

- [ ] **Step 2: Build verify**

```bash
go build ./pkg/cloudprovider/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add pkg/cloudprovider/cloudprovider.go
git commit -m "fix: resolve #8 TagManagedBy value mismatch, List() and Create() now use same tag value"
```

---

## Task 3: Add `DescribeAvailableResource` to ECSClient

**Files:**
- Modify: `pkg/clients/ecs.go`
- Modify: `pkg/providers/instance/instance_test.go` (add stub to MockECSClient)

**Interfaces:**
- Produces: `ECSClient.DescribeAvailableResource(ctx, *ecs.DescribeAvailableResourceRequest) (*ecs.DescribeAvailableResourceResponse, error)`
- Consumed by: Task 4 (`instancetype.Provider.getInventory()`)

- [ ] **Step 1: Add to ECSClient interface**

In `pkg/clients/ecs.go`, add to the `ECSClient` interface after `DescribeZones`:

```go
// Inventory operations
DescribeAvailableResource(ctx context.Context, request *ecs.DescribeAvailableResourceRequest) (*ecs.DescribeAvailableResourceResponse, error)
```

- [ ] **Step 2: Implement in DefaultECSClient**

Add after the `DescribeZones` implementation:

```go
// DescribeAvailableResource implements ECSClient interface
func (c *DefaultECSClient) DescribeAvailableResource(ctx context.Context, request *ecs.DescribeAvailableResourceRequest) (*ecs.DescribeAvailableResourceResponse, error) {
	return c.client.DescribeAvailableResource(request)
}
```

- [ ] **Step 3: Add stub to MockECSClient in instance_test.go**

In `pkg/providers/instance/instance_test.go`, add to `MockECSClient`:

```go
func (m *MockECSClient) DescribeAvailableResource(ctx context.Context, request *ecs.DescribeAvailableResourceRequest) (*ecs.DescribeAvailableResourceResponse, error) {
	// Not used in instance tests; satisfy interface
	return nil, errors.New("not implemented")
}
```

- [ ] **Step 4: Build all packages**

```bash
go build ./...
```

Expected: no errors (all ECSClient implementors now have the new method).

- [ ] **Step 5: Run existing tests**

```bash
go test ./pkg/providers/instance/... -count=1
```

Expected: all PASS (stub method is never called by existing tests).

- [ ] **Step 6: Commit**

```bash
git add pkg/clients/ecs.go pkg/providers/instance/instance_test.go
git commit -m "feat: add DescribeAvailableResource to ECSClient interface for inventory querying"
```

---

## Task 4: Integrate Per-Zone Inventory into `instancetype.Provider`

**Files:**
- Modify: `pkg/providers/instancetype/instancetype.go`

**Interfaces:**
- Consumes: `ECSClient.DescribeAvailableResource` (from Task 3)
- Produces: `ZoneInfo.ChargeTypes []string` — each zone now carries which charge types have stock

This is the core inventory fix. `getInventory()` calls `DescribeAvailableResource` twice (PostPaid + SpotAsPriceGo) and returns a `map[instanceTypeID]map[zoneID][]string` of available charge types. `List()` uses this to set per-zone `ChargeTypes` on each instance type. If inventory is unavailable, falls back to both charge types (optimistic).

- [ ] **Step 1: Extend ZoneInfo**

In `pkg/providers/instancetype/instancetype.go`, change `ZoneInfo`:

```go
// ZoneInfo represents information about an instance type in a specific zone
type ZoneInfo struct {
	Available   bool
	ChargeTypes []string // ECS charge types with stock: "PostPaid", "SpotAsPriceGo"
}
```

- [ ] **Step 2: Add inventory constants**

After the package imports, add:

```go
const (
	chargeTypePostPaid      = "PostPaid"
	chargeTypeSpotAsPriceGo = "SpotAsPriceGo"

	stockStatusWithStock       = "WithStock"
	stockStatusClosedWithStock = "ClosedWithStock"
)
```

- [ ] **Step 3: Add `getInventory()` method**

Add this method to `Provider` after `getAvailableZones()`:

```go
// getInventory fetches per-instance-type per-zone availability from DescribeAvailableResource.
// Returns map[instanceTypeID]map[zoneID][]chargeType.
// On error returns nil (caller treats nil as "optimistic: all charge types available").
func (p *Provider) getInventory(ctx context.Context) map[string]map[string][]string {
	logger := log.FromContext(ctx)

	cacheKey := "inventory"
	if cachedValue, exists := p.getCachedValue(cacheKey); exists {
		if inv, ok := cachedValue.(map[string]map[string][]string); ok {
			return inv
		}
	}

	inventory := make(map[string]map[string][]string)

	for _, chargeType := range []string{chargeTypePostPaid, chargeTypeSpotAsPriceGo} {
		request := &ecs.DescribeAvailableResourceRequest{
			RegionId:            tea.String(p.region),
			DestinationResource: tea.String("InstanceType"),
			InstanceChargeType:  tea.String(chargeType),
		}
		resp, err := p.ecsClient.DescribeAvailableResource(ctx, request)
		if err != nil {
			logger.Error(err, "failed to describe available resource, using optimistic availability", "chargeType", chargeType)
			return nil // caller treats nil as "all charge types available"
		}
		if resp == nil || resp.Body == nil || resp.Body.AvailableZones == nil {
			continue
		}
		for _, az := range resp.Body.AvailableZones.AvailableZone {
			if az == nil || az.ZoneId == nil || az.AvailableResources == nil {
				continue
			}
			zoneID := *az.ZoneId
			for _, ar := range az.AvailableResources.AvailableResource {
				if ar == nil || ar.Type == nil || *ar.Type != "InstanceType" || ar.SupportedResources == nil {
					continue
				}
				for _, sr := range ar.SupportedResources.SupportedResource {
					if sr == nil || sr.Value == nil || sr.StatusCategory == nil {
						continue
					}
					status := *sr.StatusCategory
					if status != stockStatusWithStock && status != stockStatusClosedWithStock {
						continue
					}
					itID := *sr.Value
					if inventory[itID] == nil {
						inventory[itID] = make(map[string][]string)
					}
					inventory[itID][zoneID] = append(inventory[itID][zoneID], chargeType)
				}
			}
		}
	}

	p.setCachedValue(cacheKey, inventory)
	return inventory
}
```

Note: requires `ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"` and `"github.com/alibabacloud-go/tea/tea"` imports — both are already present in the file.

- [ ] **Step 4: Update `List()` to use inventory**

In `List()`, after the loop that calls `convertECSInstanceType`, add inventory-based zone annotation. Find the loop that calls `convertECSInstanceType` and modify it:

Current pattern in `List()`:
```go
zones, err := p.getAvailableZones(ctx)
// ...
for _, ecsType := range response.Body.InstanceTypes.InstanceType {
    // ...
    it := p.convertECSInstanceType(ecsType, zones)
    // ...
}
```

Change to:

```go
zones, err := p.getAvailableZones(ctx)
if err != nil {
    return nil, err
}

// Fetch per-instance-type inventory; nil means "optimistic: all charge types"
inventory := p.getInventory(ctx)

for _, ecsType := range response.Body.InstanceTypes.InstanceType {
    if ecsType == nil || ecsType.InstanceTypeId == nil {
        continue
    }
    itID := *ecsType.InstanceTypeId

    // Build zone map filtered by inventory
    itZones := make(map[string]ZoneInfo)
    for zoneID, zoneInfo := range zones {
        if !zoneInfo.Available {
            continue
        }
        var chargeTypes []string
        if inventory == nil {
            // API unavailable: optimistic fallback — assume both charge types have stock
            chargeTypes = []string{chargeTypePostPaid, chargeTypeSpotAsPriceGo}
        } else if invZones, ok := inventory[itID]; ok {
            chargeTypes = invZones[zoneID] // may be nil if no stock
        }
        if len(chargeTypes) == 0 {
            continue // this instance type has no stock in this zone
        }
        itZones[zoneID] = ZoneInfo{Available: true, ChargeTypes: chargeTypes}
    }

    it := p.convertECSInstanceType(ecsType, itZones)
    if it == nil {
        continue
    }
    instanceTypes = append(instanceTypes, it)
}
```

- [ ] **Step 5: Verify Filter() still works**

`Filter()` in instancetype.go checks `zoneInfo.Available` — confirm that still works with the extended struct (it does, `Available` field is unchanged).

Run:
```bash
go build ./pkg/providers/instancetype/...
```

Expected: no errors.

- [ ] **Step 6: Run tests**

```bash
go test ./pkg/providers/instancetype/... -count=1 -v 2>&1 | grep -E "PASS|FAIL|RUN"
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/providers/instancetype/instancetype.go
git commit -m "feat: resolve #8 use DescribeAvailableResource to filter instance type availability per zone and charge type"
```

---

## Task 5: Fix `Offering.Requirements` in `cloudprovider.GetInstanceTypes()`

**Files:**
- Modify: `pkg/cloudprovider/cloudprovider.go`

**Interfaces:**
- Consumes: `ZoneInfo.ChargeTypes []string` (from Task 4)

Populate each offering's `Requirements` with `topology.kubernetes.io/zone` and `karpenter.sh/capacity-type`. Without this, Karpenter's topology spread scheduling and capacity-type filtering are both broken — every instance type looks compatible with every zone+capacity-type combination.

Each (zone, chargeType) pair becomes one offering. A `ZoneInfo` with `ChargeTypes: ["PostPaid", "SpotAsPriceGo"]` produces two offerings for that zone.

- [ ] **Step 1: Add `scheduling` import**

In `pkg/cloudprovider/cloudprovider.go`, add to the import block:

```go
"sigs.k8s.io/karpenter/pkg/scheduling"
```

`coreapis` is already imported as `coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"` — use it for `coreapis.CapacityTypeLabelKey`, `coreapis.CapacityTypeOnDemand`, `coreapis.CapacityTypeSpot`.

- [ ] **Step 2: Add charge-type mapping helper**

Add this private function before `buildInstanceTags`:

```go
// ecsToKarpenterCapacityType maps ECS InstanceChargeType to Karpenter capacity-type label.
func ecsToKarpenterCapacityType(ecsChargeType string) string {
	switch ecsChargeType {
	case "SpotAsPriceGo":
		return coreapis.CapacityTypeSpot
	default: // "PostPaid" and anything unknown → on-demand
		return coreapis.CapacityTypeOnDemand
	}
}
```

- [ ] **Step 3: Replace the offerings creation block**

Find the current offerings block in `GetInstanceTypes()` (~line 420-431):

```go
// Create offerings from zones
offerings := make(cloudprovider.Offerings, 0)
for zoneID, zoneInfo := range it.Zones {
    if availableZones[zoneID] && zoneInfo.Available {
        offering := &cloudprovider.Offering{
            Price:     0.0, // Pricing not implemented yet
            Available: zoneInfo.Available,
        }
        offerings = append(offerings, offering)
    }
}
```

Replace with:

```go
// Create one offering per (zone, chargeType) combination.
// Requirements are required for Karpenter's topology spread and capacity-type scheduling.
offerings := make(cloudprovider.Offerings, 0)
for zoneID, zoneInfo := range it.Zones {
    if !availableZones[zoneID] || !zoneInfo.Available {
        continue
    }
    for _, chargeType := range zoneInfo.ChargeTypes {
        offerings = append(offerings, &cloudprovider.Offering{
            Requirements: scheduling.NewRequirements(
                scheduling.NewRequirement(corev1.LabelTopologyZone, corev1.NodeSelectorOpIn, zoneID),
                scheduling.NewRequirement(coreapis.CapacityTypeLabelKey, corev1.NodeSelectorOpIn, ecsToKarpenterCapacityType(chargeType)),
            ),
            Price:     0.0, // Pricing not implemented yet
            Available: true,
        })
    }
}
```

- [ ] **Step 4: Build**

```bash
go build ./pkg/cloudprovider/...
```

Expected: no errors. If `scheduling` is unused, the compiler will catch it — verify the import was added.

- [ ] **Step 5: Run full test suite**

```bash
go test ./... -count=1 2>&1 | grep -E "FAIL|ok|---"
```

Expected: all packages `ok`, no `FAIL`.

- [ ] **Step 6: Commit**

```bash
git add pkg/cloudprovider/cloudprovider.go
git commit -m "fix: resolve #8 populate Offering.Requirements with zone and capacity-type, enabling topology-aware scheduling"
```

---

## Task 6: End-to-End Verification

- [ ] **Step 1: Full build and vet**

```bash
go build ./... && go vet ./...
```

Expected: no output (success).

- [ ] **Step 2: Full test suite**

```bash
go test ./pkg/... -count=1 2>&1 | tail -20
```

Expected: all `ok`, no `FAIL`.

- [ ] **Step 3: Manual inventory sanity check**

Verify the DescribeAvailableResource response traversal logic by logging:

```bash
# In a test environment, confirm that getInventory() returns non-nil results
# with expected structure: map[ecs.c6.xlarge][cn-hangzhou-h][PostPaid]
```

- [ ] **Step 4: Push branch**

```bash
git push origin fix/issue-8-overprovisioning
```

---

## Verification Checklist

| What | How |
|------|-----|
| TagManagedBy fixed | `git grep '"karpenter"'` shows both List() (line ~295) and buildInstanceTags (line ~604) use `"karpenter"` |
| spec.limits present | `cat examples/default-nodepool.yaml \| grep -A2 limits` |
| DescribeAvailableResource in interface | `go build ./...` succeeds with all MockECSClient stubs |
| Inventory filters no-stock zones | With DescribeAvailableResource returning WithoutStock for zone A, instance type has no offering for zone A |
| Offering.Requirements populated | `scheduling.NewRequirements(zone, capacityType)` called in GetInstanceTypes offerings loop |
| Capacity-type filtering works | NodePool with `capacity-type: spot` sees only spot offerings |
| Zone topology spread works | Pods with topologySpreadConstraints across zones correctly bin-pack |

## Summary of Root Causes Fixed

| Bug | Fix | File |
|-----|-----|------|
| No `spec.limits` → unlimited provisioning | Add conservative defaults | `examples/default-nodepool.yaml` |
| `TagManagedBy="true"` but `List()` queries `"karpenter"` → GC broken | `"true"` → `"karpenter"` in buildInstanceTags | `pkg/cloudprovider/cloudprovider.go:604` |
| All zones marked Available=true regardless of stock | Integrate `DescribeAvailableResource` into `instancetype.Provider.List()` | `pkg/clients/ecs.go`, `pkg/providers/instancetype/instancetype.go` |
| `Offering.Requirements` nil → topology scheduling degenerate | Populate per (zone, chargeType) offering | `pkg/cloudprovider/cloudprovider.go:420-431` |
