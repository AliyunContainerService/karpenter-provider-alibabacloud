# Karpenter AlibabaCloud Provider — 能力差距合并计划 Spec

> 目标：将 `feature0622/provider-gap-impl`（18 项能力差距、21 个 commit）中相对社区 AWS Provider(v1.12.1) 补齐的能力，**逐项、分批**合并进 `opensource-main`，并接入 E2E suite 验证。
> 本文档同时作为**合并与测试进展看板**，每完成一项请更新对应状态。

---

## 0. 基线与拓扑事实（实测）

| 项 | 值 | 说明 |
|---|---|---|
| 合并作业分支 | `feature/provider-gap-merge` | 基线 = `origin/opensource-main` (`66b419c`) |
| 能力来源分支 | `origin/feature0622/provider-gap-impl` (`58c864d`) | 21 commit / 18 issue |
| 分叉点 merge-base | `6bfa3cb8` | 两分支共同祖先 |
| gap-impl 领先分叉点 | **21 commits** | 18 issue（09、18 拆 P0/P1/P2） |
| gap-impl 落后主线 | **40 commits** | 主线已独立实现 #2~#10 多项 + E2E 框架 |
| 直接 merge 冲突 | **24 文件 / 74 冲突块**（核心 54 / 测试 20） | 实测 `git merge --no-commit` |

**核心结论：禁止整分支 merge，按能力逐项 cherry-pick / 重写。**

### 冲突根因（方向性冲突，非简单漂移）
- **限流重试**：主线 `0f8078e`/`4426b19` 已**移除 provider 层限流重试**（交给 NodeClaim reconciler 指数退避），gap-impl 仍保留旧 `for attempt` 重试 → 必须采纳主线方案，丢弃 gap-impl 重试。
- **所有权 tag 语义**：`labels.go` 主线 `TagManagedByValue="true"`，gap-impl `"karpenter"` → 契约级对撞，需统一命名 + legacy 迁移。
- **cloudprovider.go**：主线重构 + gap-impl 加 ~810 行，单文件 11 冲突块 → 需在主线新结构上重写。

---

## 1. 18 项差距 → 处置决策总览

| # | Issue | 能力（差距）简述 | 主线现状 | gap-impl 现状 | 处置结论 |
|---|---|---|---|---|---|
| 01 | selector-semantics | selector 暴露字段多于 resolver 实际消费（VSwitch.zoneID、SG.name、Image.name/tags/owner*），CRD 缺 CEL/webhook 校验 | 部分（zone 过滤已修 `801ca1f`） | 完整字段映射 + 校验 | 适配合并（在主线校验上补字段消费） |
| 02 | nodeclass-readiness | NodeClass readiness 缺 condition 驱动 + preflight | 已实现 | 有 | 忽略（主线已有） |
| 03 | ram-role | RunInstances 未挂 RAM Role | 无 | RamRoleName | 合并（低风险，批1） |
| 04 | launch-template | 未解析 LaunchTemplate ID/Version 到 create | 无 | 有 + drift | 适配合并（批2） |
| 05 | metadata-options | 未传 IMDS MetadataOptions | 无 | 有 + drift | 合并（批1） |
| 06 | capacity-reservation | 无容量预留私有池 offering | 无 | 私有池 + reserved offering + drift | 适配合并（批2） |
| 07 | deployment-set | 无 DeploymentSet 部署集放置 | 无 | 有 + placement readiness + drift | 合并（批1，注意文件多） |
| 08 | multi-security-group | create 仅用 SecurityGroupIDs[0]（单安全组） | 单安全组 | 全安全组 + exact-set drift | 合并（批1，首推） |
| 09-P0 | disk-options | 磁盘参数不全（加密/性能级别/多数据盘等） | 不全 | 全磁盘选项 + drift | 合并（批1） |
| 09-P1 | instance-store-raid0 | 无 InstanceStore RAID0 容量/调度/bootstrap | 无 | 有 | 适配合并（批2，依赖 09-P0） |
| 10 | instance-type-offering | offering 模型不完整（价格未接、候选回退缺） | 已实现（`bca87b8` #8 offering/inventory） | 有 | 忽略（主线已有，价格另见 11） |
| 11 | pricing-refresh | 无定价 reader/updater/refresh controller，价格恒 0 | 无（`Price: 0.0`） | 静态兜底 + refresh controller | 适配合并（批2） |
| 12 | unavailable-cache | 无不可用机型缓存，创建失败无分类 | 无 | 失败分类 + unavailable cache | 适配合并（批2，配合 13） |
| 13 | candidate-fallback | 无确定性候选生成 + 有界回退循环 | 部分（vswitch fallback `fb8b0c5`） | 机型×AZ×容量类型确定性回退 | 适配合并（批2） |
| 14 | interruption | interruption controller 仅空 stub，未注册 | stub | MNS consumer + parser + resolver | 重写落地（批3，controller 注册耦合） |
| 15 | pod-density | Terway pod ENI 密度计算缺共享实现 | 无 | 共享密度计算器 | 合并（批1，相对自包含） |
| 16 | metrics-events | 仅 batcher metrics(7)，缺 ~10 业务 metrics + event 契约 | 缺 | 集中 metrics + wrapped client + recorder | 重写落地（批3，全局埋点耦合） |
| 17 | options-and-gates | 无 provider options / feature gates | 无 | options + gates + 校验 | 适配合并（批2，被 06/14/18-P2 依赖） |
| 18-P0 | tags-list-gc | 三处所有权 tag 不一致，List/Delete/GC 无强所有权校验 | 不一致 | canonical tag 契约贯穿 launch/List/Get/Delete/GC + legacy 迁移 | 重写落地（批3，契约级，最高危） |
| 18-P1 | ownership-predicate | Get 与 tag 修复路径无所有权判定 | 无 | Get ownership predicate + 修复边界 | 重写落地（批3，依赖 18-P0） |
| 18-P2 | orphan-gc | 无主动孤儿实例 GC | 无 | orphangc controller + feature gate + dry-run | 重写落地（批3，依赖 17 + 18-P0/P1） |

图例：合并=直接 cherry-pick / 适配合并=rebase 后重写冲突处 / 重写落地=按主线语义重写 / 忽略=主线已有

---

## 2. 合并批次与顺序（严格按依赖）

> ⚠️ 重要修正（实测）：原计划把 07/15 列为「批1 自包含」，实际 cherry-pick 发现二者都强依赖 **17（options/feature gates）与 16（metrics events）**，07 还依赖 **04/06** 的 readiness reconcile 重构（`reconcileLaunchTemplate`/`reconcileCapacityReservations`/`ValidationSucceeded`）。因此已把 **17 提前为整体基石**，07/15 顺延到 16/17（及 04/06）之后。

### 批 1 · 低风险自包含（instance.go request 构造处加字段 + CRD 加字段，与主线重构区隔清晰）

| 序 | Issue | 源 commit | 依赖 | 状态 |
|---|---|---|---|---|
| 1 | 08 多安全组 | `69bfa93` | — | ✅ 已合并 `69127b7`（仅取多安全组；drift 已随主线；测试由 instance 层覆盖，cloudprovider 层测试脚手架依赖前置项故未移植） |
| 2 | 03 RAM Role | `71f4495` | — | ✅ 已合并 `4ce4eb4` |
| 3 | 05 MetadataOptions/IMDS | `bebbf5b` | — | ✅ 已合并 `3c712fa`（hash 采用主线 inline+MetadataOptions；删除混入的 nodeclass_hash.go） |
| 4 | 09-P0 磁盘全选项 | `b4ffa7c` | — | ✅ 已合并 `0374f6c`（磁盘全选项+drift；hash 走主线 inline，追加 SystemDisk/DataDisks/InstanceStorePolicy 字段） |

### 批 2 · 基石 + 中风险适配（先落 17/16 基石，再在主线新结构上重写）
| 序 | Issue | 源 commit | 依赖 | 状态 |
|---|---|---|---|---|
| 5 | 17 provider options/feature gates | `5c76cec` | — | ✅ 已合并 `47018f9`（基石；仅 values.yaml 冲突，合并 core+provider gates；options/featuregates 单测通过） |
| 6 | 01 selector 语义与字段消费 | `36ca169` | — | 待办 |
| 7 | 04 LaunchTemplate | `2c15efe` | 17 | 待办 |
| 8 | 06 容量预留私有池 | `2f5f2da` | 17 | 待办 |
| 9 | 09-P1 InstanceStore RAID0 | `88f4c34` | 09-P0, 17 | 待办 |
| 10 | 11 pricing refresh | `3ceaa65` | 17 | 待办 |
| 11 | 12 不可用机型缓存 | `a0dad22` | 17 | 待办 |
| 12 | 13 候选回退 | `30bc787` | 12 | 待办 |
| 13 | 07 DeploymentSet | `4384e5a` | 17, 04, 06 | 待办（原批1，实测依赖 readiness reconcile 重构，顺延） |
| 14 | 15 Terway pod 密度 | `431196e` | 17, 16 | 待办（原批1，实测依赖 options + metrics events，顺延） |

### 批 3 · 契约级/全局耦合需重写（不能 cherry-pick）
| 序 | Issue | 源 commit | 依赖 | 状态 |
|---|---|---|---|---|
| 15 | 16 provider metrics/events | `be54a28` | 17 | 待办（提前：07/15/14 都依赖其 EventReason/metrics 定义） |
| 16 | 18-P0 所有权 tag 契约 + List/Delete/GC 守卫 | `ac8abbb` | — | 待办 |
| 17 | 18-P1 Get 所有权 predicate | `c9aad97` | 18-P0 | 待办 |
| 18 | 18-P2 孤儿实例 GC（gate+dry-run） | `58c864d` | 17, 18-P0/P1 | 待办 |
| 19 | 14 阿里云中断事件处理 | `ab0dbd9` | 17, 16 | 待办 |

### 忽略（主线已实现，不合并）
- 02 nodeclass-readiness（`b449add`）
- 10 instance-type-offering（`4af683d`）

---

## 3. 每项合并 SOP（每个 issue 都走一遍）

1. `git checkout feature/provider-gap-merge`
2. `git cherry-pick <commit>`（批3 为手动重写）
3. 解冲突：**方向冲突一律采纳主线**（限流重试丢弃、tag 命名统一 true 并做迁移）
4. `make generate` 重生成 CRD / deepcopy（若改了 types）
5. `go build ./... && go vet ./...`
6. 单测：`go test ./pkg/...`（对应包）
7. E2E：接入 `test/pkg/cs` suite 单项验证
8. 更新本 spec 状态列 + 记录到「进展日志」

---

## 4. 进展日志

| 日期 | Issue | 动作 | 冲突/结果 | 单测 | E2E |
|---|---|---|---|---|---|
| 2026-08-30 | — | 建分支 + 写 spec | 基线 `66b419c` | — | — |
| 2026-08-30 | 08 多安全组 | cherry-pick `69bfa93` | 3 冲突文件；cloudprovider.go 仅取多安全组、drift(exact-set)随主线；instance_test 去掉 MetadataOptions 用例；cloudprovider_test 保持主线(脚手架依赖前置项) | ✅ instance/cloudprovider/batcher/securitygroups 通过 | ⬜ 待接 |
| 2026-08-30 | 03 RAM Role | cherry-pick `71f4495` | 4 冲突；instance.go 并列保留 Ipv6+RAMRole；validation_test 统一辅助函数名 ptrForUnit；cloudprovider_test 保持主线 | ✅ 通过 | ⬜ 待接 |
| 2026-08-30 | 05 MetadataOptions | cherry-pick `bebbf5b` | 5 冲突；关键决策：删除 gap 的 nodeclass_hash.go，calculateNodeClassHash/computeHash 均回归主线 inline 并追加 MetadataOptions 字段，避免双 hash 实现导致 drift 误判；补回 sha256/hex/json import | ✅ 通过；envtest(status/hash) suite 因本机缺 etcd/apiserver 失败(与改动无关，基线同样失败) | ⬜ 待接 |
| 2026-08-30 | 09-P0 磁盘全选项 | cherry-pick `b4ffa7c` | 6 冲突；新增 disks_normalize.go(NormalizeDisks)；cloudprovider.go 采纳 NormalizeDisks 生成全磁盘 baseOpts；再次删除 gap 混入的 nodeclass_hash.go，两处 hash inline 追加 SystemDisk/DataDisks/InstanceStorePolicy；cloudprovider_test 重置主线版；suite_test 取主线(已覆盖磁盘drift)；instance_test 仅保留 TestCreateDiskOptionsOmitSendSemantics(去重 TestCreateMetadataOptions)；测试辅助名 loPtr→ptrForUnit | ✅ cloudprovider/instance/v1alpha1 通过 | ⬜ 待接 |
| 2026-08-30 | 07/15 试合并 | cherry-pick `4384e5a`/`431196e` 后**回退** | 实测二者非自包含：07 依赖 04/06 的 readiness reconcile 重构 + `ValidationSucceeded`/`PlacementReady` 条件；15 依赖 17 的 options（PodDensity/Terway/FeatureGates）+ 16 的 `EventReasonPodDensity*`。均 abort，重排到 16/17（及 04/06）之后 | — | — |
| 2026-08-30 | 17 options/feature gates | cherry-pick `5c76cec` | 基石落地；仅 charts/values.yaml 1 处冲突（合并 core featureGates 注释 + provider pricing/podDensity/terway/featureGates 配置块）；options.go/operator.go/unavailable_offerings.go 自动合并 | ✅ options/featuregates 单测通过 | ⬜ 待接 |

---

## 5. 验收标准（DoD）
- [ ] 批1~批3 所有非忽略 issue 合并完成，`go build` / `go vet` 通过
- [ ] 各包单测通过；关键路径 E2E suite 通过
- [ ] 所有权 tag 契约统一（无多集群误删风险），legacy 迁移可回滚
- [ ] provider 层无限流重试残留（与主线一致）
- [ ] feature gates 默认关闭高风险能力（孤儿 GC dry-run 优先）
