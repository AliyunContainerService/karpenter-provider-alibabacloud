# 快速开始

本文档将指导您如何快速部署和使用 Alibaba Cloud Karpenter Provider。

## 前置条件

1. 一个运行中的 Kubernetes 集群 (推荐使用阿里云 ACK)
2. 配置好 kubectl 并能够访问集群
3. 阿里云账号和访问凭证 (AccessKey ID 和 AccessKey Secret)
4. 集群所在的 VPC、VSwitch 和安全组资源

## 安装

### 1. 克隆仓库

```bash
git clone https://github.com/AliyunContainerService/karpenter-provider-alibabacloud.git
cd karpenter-provider-alibabacloud
```

### 2. 配置阿里云凭证

您可以通过以下方式之一配置阿里云凭证：

#### 方式一：环境变量

```bash
export ALIBABA_CLOUD_ACCESS_KEY_ID=your-access-key-id
export ALIBABA_CLOUD_ACCESS_KEY_SECRET=your-access-key-secret
```

#### 方式二：阿里云 CLI 配置文件

```bash
# 安装阿里云 CLI
# 配置凭证
aliyun configure
```

### 3. 安装 CRD

```bash
kubectl apply -f charts/karpenter/crds/
```

### 4. 部署 Karpenter Controller

```bash
# 创建命名空间
kubectl create namespace karpenter

# 部署控制器
kubectl apply -f charts/karpenter/templates/deployment.yaml -n karpenter
```

或者使用 Helm 部署：

```bash
helm install karpenter charts/karpenter -n karpenter --create-namespace
```

## 配置 ECSNodeClass

创建一个 ECSNodeClass 资源来定义节点配置：

```yaml
apiVersion: karpenter.alibabacloud.com/v1alpha1
kind: ECSNodeClass
metadata:
  name: default
spec:
  # VSwitch 选择器
  vSwitchSelectorTerms:
    - tags:
        karpenter.sh/discovery: my-cluster

  # 安全组选择器
  securityGroupSelectorTerms:
    - tags:
        karpenter.sh/discovery: my-cluster

  # 镜像选择器
  imageSelectorTerms:
    - id: "aliyun_3_x64_20G_container_optimized_alibase_20250629.vhd"
```

## 配置 NodePool

创建一个 NodePool 资源来定义节点池配置：

```yaml
apiVersion: karpenter.sh/v1
kind: NodePool
metadata:
  name: default
spec:
  template:
    spec:
      requirements:
        - key: karpenter.sh/capacity-type
          operator: In
          values: ["on-demand"]
        - key: kubernetes.io/arch
          operator: In
          values: ["amd64"]
        - key: node.kubernetes.io/instance-type
          operator: In
          values: 
            - ecs.g6.large
            - ecs.g6.xlarge
            - ecs.c6.large
            - ecs.c6.xlarge
        - key: topology.kubernetes.io/zone
          operator: In
          values:
            - cn-hongkong-b
            - cn-hongkong-c
            - cn-hongkong-d
      nodeClassRef:
        name: default
        group: karpenter.alibabacloud.com
        kind: ECSNodeClass
  limits:
    cpu: "1000"
    memory: 1000Gi
  disruption:
    consolidationPolicy: WhenEmptyOrUnderutilized
    consolidateAfter: 720h # 30 days
```

## 部署资源配置

```bash
kubectl apply -f examples/default-ecsnodeclass.yaml
kubectl apply -f examples/default-nodepool.yaml
```

## 验证部署

检查 ECSNodeClass 是否创建成功：

```bash
kubectl get ecsnodeclass
```

检查 NodePool 是否创建成功：

```bash
kubectl get nodepool
```

检查 Karpenter 控制器是否正常运行：

```bash
kubectl get pods -n karpenter
```

## 测试自动扩缩容

创建一个测试工作负载：

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: inflate
spec:
  replicas: 4
  selector:
    matchLabels:
      app: inflate
  template:
    metadata:
      labels:
        app: inflate
    spec:
      containers:
        - name: inflate
          image: registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9
          resources:
            requests:
              cpu: "2"
              memory: "4Gi"
```

```bash
kubectl apply -f examples/inflate.yaml
```

扩展副本数以触发自动扩缩容：

```bash
kubectl scale deployment inflate --replicas 5
```

观察节点自动创建：

```bash
kubectl get nodes
```

## 清理资源

删除测试工作负载：

```bash
kubectl delete deployment inflate
```

删除 NodePool 和 ECSNodeClass：

```bash
kubectl delete nodepool default
kubectl delete ecsnodeclass default
```

卸载 Karpenter：

```bash
helm uninstall karpenter -n karpenter
kubectl delete namespace karpenter
```