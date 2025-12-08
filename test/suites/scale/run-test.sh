#!/bin/bash

# Scale 测试运行脚本
# 用于快速运行 Karpenter Scale 测试

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 打印带颜色的消息
print_info() {
    echo -e "${BLUE}ℹ ${1}${NC}"
}

print_success() {
    echo -e "${GREEN}✓ ${1}${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠ ${1}${NC}"
}

print_error() {
    echo -e "${RED}✗ ${1}${NC}"
}

# 检查必需的环境变量
check_env_vars() {
    print_info "检查环境变量..."
    
    local required_vars=(
        "TEST_REGION"
        "ALIBABA_CLOUD_ACCESS_KEY_ID"
        "ALIBABA_CLOUD_ACCESS_KEY_SECRET"
        "TEST_CLUSTER_ID"
        "TEST_CLUSTER_ENDPOINT"
    )
    
    local missing_vars=()
    
    for var in "${required_vars[@]}"; do
        if [ -z "${!var}" ]; then
            missing_vars+=("$var")
        fi
    done
    
    if [ ${#missing_vars[@]} -ne 0 ]; then
        print_error "缺少必需的环境变量:"
        for var in "${missing_vars[@]}"; do
            echo "  - $var"
        done
        echo ""
        echo "请设置环境变量："
        echo "  export TEST_REGION=\"cn-hangzhou\""
        echo "  export ALIBABA_CLOUD_ACCESS_KEY_ID=\"<your-key-id>\""
        echo "  export ALIBABA_CLOUD_ACCESS_KEY_SECRET=\"<your-key-secret>\""
        echo "  export TEST_CLUSTER_ID=\"<cluster-id>\""
        echo "  export TEST_CLUSTER_ENDPOINT=\"<cluster-endpoint>\""
        exit 1
    fi
    
    print_success "环境变量检查通过"
}

# 检查 kubectl 连接
check_kubectl() {
    print_info "检查 Kubernetes 连接..."
    
    if ! command -v kubectl &> /dev/null; then
        print_error "kubectl 未安装"
        exit 1
    fi
    
    if ! kubectl cluster-info &> /dev/null; then
        print_error "无法连接到 Kubernetes 集群"
        exit 1
    fi
    
    print_success "Kubernetes 连接正常"
}

# 检查 Karpenter Controller
check_karpenter() {
    print_info "检查 Karpenter Controller..."
    
    if ! kubectl get deployment -n karpenter karpenter &> /dev/null; then
        print_warning "未找到 Karpenter Controller，测试可能失败"
    else
        local ready=$(kubectl get deployment -n karpenter karpenter -o jsonpath='{.status.readyReplicas}')
        if [ "$ready" == "0" ] || [ -z "$ready" ]; then
            print_warning "Karpenter Controller 未就绪"
        else
            print_success "Karpenter Controller 运行正常"
        fi
    fi
}

# 检查 CRDs
check_crds() {
    print_info "检查 CRDs..."
    
    local required_crds=(
        "nodepools.karpenter.sh"
        "nodeclaims.karpenter.sh"
        "ecsnodeclasses.karpenter.alibabacloud.com"
    )
    
    for crd in "${required_crds[@]}"; do
        if ! kubectl get crd "$crd" &> /dev/null; then
            print_error "CRD $crd 未安装"
            exit 1
        fi
    done
    
    print_success "CRDs 检查通过"
}

# 清理旧的测试资源
cleanup_old_resources() {
    print_info "清理旧的测试资源..."
    
    # 删除测试 Deployments
    kubectl delete deployment -l testing/type --all-namespaces --ignore-not-found=true &> /dev/null || true
    
    # 删除测试 NodePools
    kubectl delete nodepool -l testing/cluster --ignore-not-found=true &> /dev/null || true
    
    # 删除测试 ECSNodeClasses
    kubectl delete ecsnodeclass -l testing/cluster --ignore-not-found=true &> /dev/null || true
    
    sleep 5
    print_success "清理完成"
}

# 运行测试
run_tests() {
    local test_label="$1"
    local test_description="$2"
    
    print_info "运行 $test_description..."
    echo ""
    
    cd "$(dirname "$0")"
    
    if [ -z "$test_label" ]; then
        # 运行所有测试
        if ginkgo -v --timeout=60m ./; then
            print_success "$test_description 完成"
            return 0
        else
            print_error "$test_description 失败"
            return 1
        fi
    else
        # 运行特定标签的测试
        if ginkgo -v --timeout=60m --label-filter="$test_label" ./; then
            print_success "$test_description 完成"
            return 0
        else
            print_error "$test_description 失败"
            return 1
        fi
    fi
}

# 显示帮助信息
show_help() {
    cat << EOF
Scale 测试运行脚本

用法: $0 [选项]

选项:
    -h, --help              显示帮助信息
    -a, --all               运行所有测试（默认）
    -p, --provisioning      只运行 Provisioning 测试
    -d, --deprovisioning    只运行 Deprovisioning 测试
    -n, --node-dense        只运行 node-dense 测试
    -o, --pod-dense         只运行 pod-dense 测试
    -c, --consolidation     只运行 consolidation 测试
    -e, --emptiness         只运行 emptiness 测试
    -x, --expiration        只运行 expiration 测试
    -r, --drift             只运行 drift 测试
    --skip-checks           跳过前置检查
    --skip-cleanup          跳过清理旧资源
    --verbose               显示详细输出

示例:
    $0                      # 运行所有测试
    $0 -p                   # 只运行 Provisioning 测试
    $0 -n                   # 只运行 node-dense 测试
    $0 --skip-cleanup -d    # 跳过清理，运行 Deprovisioning 测试

EOF
}

# 主函数
main() {
    local test_label=""
    local test_description="Scale 测试"
    local skip_checks=false
    local skip_cleanup=false
    
    # 解析命令行参数
    while [[ $# -gt 0 ]]; do
        case $1 in
            -h|--help)
                show_help
                exit 0
                ;;
            -a|--all)
                test_label=""
                test_description="所有 Scale 测试"
                shift
                ;;
            -p|--provisioning)
                test_label="provisioning"
                test_description="Provisioning 测试"
                shift
                ;;
            -d|--deprovisioning)
                test_label="deprovisioning"
                test_description="Deprovisioning 测试"
                shift
                ;;
            -n|--node-dense)
                test_label="node-dense"
                test_description="Node-Dense 测试"
                shift
                ;;
            -o|--pod-dense)
                test_label="pod-dense"
                test_description="Pod-Dense 测试"
                shift
                ;;
            -c|--consolidation)
                test_label="consolidation"
                test_description="Consolidation 测试"
                shift
                ;;
            -e|--emptiness)
                test_label="emptiness"
                test_description="Emptiness 测试"
                shift
                ;;
            -x|--expiration)
                test_label="expiration"
                test_description="Expiration 测试"
                shift
                ;;
            -r|--drift)
                test_label="drift"
                test_description="Drift 测试"
                shift
                ;;
            --skip-checks)
                skip_checks=true
                shift
                ;;
            --skip-cleanup)
                skip_cleanup=true
                shift
                ;;
            --verbose)
                set -x
                shift
                ;;
            *)
                print_error "未知选项: $1"
                show_help
                exit 1
                ;;
        esac
    done
    
    echo "========================================="
    echo "  Karpenter Scale 测试套件"
    echo "========================================="
    echo ""
    
    # 执行前置检查
    if [ "$skip_checks" = false ]; then
        check_env_vars
        check_kubectl
        check_karpenter
        check_crds
        echo ""
    fi
    
    # 清理旧资源
    if [ "$skip_cleanup" = false ]; then
        cleanup_old_resources
        echo ""
    fi
    
    # 运行测试
    echo "========================================="
    echo "  开始测试"
    echo "========================================="
    echo ""
    
    if run_tests "$test_label" "$test_description"; then
        echo ""
        echo "========================================="
        print_success "测试成功完成！"
        echo "========================================="
        exit 0
    else
        echo ""
        echo "========================================="
        print_error "测试失败"
        echo "========================================="
        echo ""
        print_info "查看日志以获取更多信息"
        print_info "运行 'kubectl logs -n karpenter -l app.kubernetes.io/name=karpenter' 查看 Karpenter 日志"
        exit 1
    fi
}

# 运行主函数
main "$@"
