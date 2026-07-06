package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	slb "github.com/aliyun/alibaba-cloud-sdk-go/services/slb"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("command is required: setup, cleanup, dump, refresh-kubeconfig, ensure-nodepool, ensure-capacity-reservation, nodepools, or tasks")
	}
	switch args[0] {
	case "setup":
		return runSetup(args[1:])
	case "refresh-kubeconfig":
		return runRefreshKubeconfig(args[1:])
	case "ensure-nodepool":
		return runEnsureNodePool(args[1:])
	case "nodepools":
		return runNodePools(args[1:])
	case "delete-nodepool":
		return runDeleteNodePool(args[1:])
	case "tasks":
		return runTasks(args[1:])
	case "ensure-api-access":
		return runEnsureAPIAccess(args[1:])
	case "ensure-capacity-reservation":
		return runEnsureCapacityReservation(args[1:])
	case "cleanup":
		return runCleanup(args[1:])
	case "dump":
		return runDump(args[1:])
	case "discover-gpu":
		return runDiscoverGPU(args[1:])
	case "sweep":
		return runSweep(args[1:])
	case "render-config":
		return runRenderConfig(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runRefreshKubeconfig(args []string) error {
	fs := flag.NewFlagSet("refresh-kubeconfig", flag.ContinueOnError)
	configPath := fs.String("config", "", "deploy config path")
	manifestPath := fs.String("manifest", "", "owner manifest path")
	outputPath := fs.String("output", "", "kubeconfig output path override")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if *manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	manifest, err := LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	applyManifestRuntime(cfg, manifest)
	if manifest.ClusterID == "" {
		return fmt.Errorf("manifest cluster id is required")
	}
	kubeconfigPath := refreshKubeconfigOutputPath(cfg, manifest, *outputPath)
	client, err := newCSClient(cfg)
	if err != nil {
		return err
	}
	kubeconfig, err := getKubeconfig(client, manifest.ClusterID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(kubeconfigPath), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0600); err != nil {
		return fmt.Errorf("write kubeconfig: %w", err)
	}
	if *outputPath == "" && manifest.KubeconfigPath != kubeconfigPath {
		manifest.KubeconfigPath = kubeconfigPath
		if err := SaveManifest(*manifestPath, manifest); err != nil {
			return err
		}
	}
	fmt.Println(kubeconfigPath)
	return nil
}

func refreshKubeconfigOutputPath(cfg *Config, manifest *Manifest, outputPath string) string {
	if outputPath != "" {
		return outputPath
	}
	if cfg != nil && cfg.Kubeconfig != "" {
		return cfg.Kubeconfig
	}
	clusterName := "ack"
	if manifest != nil && manifest.ClusterName != "" {
		clusterName = manifest.ClusterName
	}
	return filepath.Join(".e2e", clusterName, "kubeconfig")
}

func runEnsureAPIAccess(args []string) error {
	fs := flag.NewFlagSet("ensure-api-access", flag.ContinueOnError)
	configPath := fs.String("config", "", "deploy config path")
	manifestPath := fs.String("manifest", "", "owner manifest path")
	acl := fs.String("acl", "", "comma-separated API server public access CIDRs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if *manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	manifest, err := LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	applyManifestRuntime(cfg, manifest)
	if manifest.ClusterID == "" {
		return fmt.Errorf("manifest cluster id is required")
	}
	client, err := newCSClient(cfg)
	if err != nil {
		return err
	}
	cidrs := splitCSV(*acl)
	if len(cidrs) == 0 {
		return fmt.Errorf("--acl must contain at least one CIDR")
	}
	if _, err := client.ModifyCluster(tea.String(manifest.ClusterID), &cs.ModifyClusterRequest{
		AccessControlList: stringPtrs(cidrs),
	}); err != nil {
		return fmt.Errorf("modify ACK cluster API access: %w", err)
	}
	slbClient, err := newSLBClient(cfg)
	if err != nil {
		return err
	}
	resp, err := client.DescribeClusterResources(tea.String(manifest.ClusterID), &cs.DescribeClusterResourcesRequest{})
	if err != nil {
		return fmt.Errorf("describe ACK cluster resources: %w", err)
	}
	if resp != nil && resp.Body != nil {
		for _, resource := range resp.Body {
			if resource == nil {
				continue
			}
			fmt.Printf("%s\t%s\t%s\t%s\n", tea.StringValue(resource.ResourceType), tea.StringValue(resource.InstanceId), tea.StringValue(resource.State), tea.StringValue(resource.ResourceInfo))
			if strings.Contains(tea.StringValue(resource.ResourceType), "SLB") && tea.StringValue(resource.InstanceId) != "" {
				if err := ensureAPISLBListenerAccess(slbClient, tea.StringValue(resource.InstanceId), cidrs); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func ensureAPISLBListenerAccess(client *slb.Client, loadBalancerID string, cidrs []string) error {
	describe := slb.CreateDescribeLoadBalancerTCPListenerAttributeRequest()
	describe.LoadBalancerId = loadBalancerID
	describe.ListenerPort = requests.NewInteger(6443)
	attr, err := client.DescribeLoadBalancerTCPListenerAttribute(describe)
	if err != nil {
		return fmt.Errorf("describe SLB TCP listener %s: %w", loadBalancerID, err)
	}
	fmt.Printf("SLB-TCP\t%s\tport=6443\tstatus=%s\taclStatus=%s\taclType=%s\taclId=%s\n", loadBalancerID, attr.Status, attr.AclStatus, attr.AclType, attr.AclId)
	if allowsAllIPv4(cidrs) {
		if strings.EqualFold(attr.AclStatus, "on") {
			update := slb.CreateSetLoadBalancerTCPListenerAttributeRequest()
			update.LoadBalancerId = loadBalancerID
			update.ListenerPort = requests.NewInteger(6443)
			update.AclStatus = "off"
			if _, err := client.SetLoadBalancerTCPListenerAttribute(update); err != nil {
				return fmt.Errorf("disable SLB TCP listener ACL %s: %w", loadBalancerID, err)
			}
		}
		return startAPISLBListener(client, loadBalancerID)
	}
	if strings.EqualFold(attr.AclStatus, "on") && attr.AclId != "" {
		if err := addSLBACLAllowlists(client, attr.AclId, cidrs); err != nil {
			return err
		}
	} else if strings.EqualFold(attr.AclStatus, "on") {
		acl := slb.CreateSetListenerAccessControlStatusRequest()
		acl.LoadBalancerId = loadBalancerID
		acl.ListenerPort = requests.NewInteger(6443)
		acl.ListenerProtocol = "tcp"
		acl.AccessControlStatus = "close"
		if _, err := client.SetListenerAccessControlStatus(acl); err != nil {
			return fmt.Errorf("disable SLB TCP listener ACL %s: %w", loadBalancerID, err)
		}
	}
	return startAPISLBListener(client, loadBalancerID)
}

func startAPISLBListener(client *slb.Client, loadBalancerID string) error {
	start := slb.CreateStartLoadBalancerListenerRequest()
	start.LoadBalancerId = loadBalancerID
	start.ListenerPort = requests.NewInteger(6443)
	start.ListenerProtocol = "tcp"
	if _, err := client.StartLoadBalancerListener(start); err != nil && !isNotFoundOrDependencyError(err) {
		return fmt.Errorf("start SLB TCP listener %s: %w", loadBalancerID, err)
	}
	return nil
}

func allowsAllIPv4(cidrs []string) bool {
	for _, cidr := range cidrs {
		if strings.TrimSpace(cidr) == "0.0.0.0/0" {
			return true
		}
	}
	return false
}

func addSLBACLAllowlists(client *slb.Client, aclID string, cidrs []string) error {
	entries := make([]map[string]string, 0, len(cidrs))
	for _, cidr := range cidrs {
		entries = append(entries, map[string]string{"entry": cidr, "comment": "karpenter-e2e"})
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	req := slb.CreateAddAccessControlListEntryRequest()
	req.AclId = aclID
	req.AclEntrys = string(data)
	if _, err := client.AddAccessControlListEntry(req); err != nil && !strings.Contains(err.Error(), "AclEntry") {
		return fmt.Errorf("add SLB ACL entries %s: %w", aclID, err)
	}
	describe := slb.CreateDescribeAccessControlListAttributeRequest()
	describe.AclId = aclID
	describe.PageSize = requests.NewInteger(100)
	resp, err := client.DescribeAccessControlListAttribute(describe)
	if err != nil {
		return fmt.Errorf("describe SLB ACL %s: %w", aclID, err)
	}
	fmt.Printf("SLB-ACL\t%s\tentries=%d\n", aclID, resp.TotalAclEntry)
	return nil
}

func splitCSV(value string) []string {
	var values []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			values = append(values, part)
		}
	}
	return values
}

func runEnsureCapacityReservation(args []string) error {
	fs := flag.NewFlagSet("ensure-capacity-reservation", flag.ContinueOnError)
	configPath := fs.String("config", "", "deploy config path")
	manifestPath := fs.String("manifest", "", "owner manifest path")
	outputEnv := fs.String("output-env", "", "environment output path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if *manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	manifest, err := LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	applyManifestRuntime(cfg, manifest)
	resources, err := DiscoverClusterResources(context.Background(), cfg, manifest.ClusterID)
	if err != nil {
		return err
	}
	id, instanceType, err := EnsureCapacityReservation(context.Background(), cfg, manifest, resources)
	if err != nil {
		return err
	}
	resources.CapacityReservationID = id
	resources.CapacityReservationInstanceType = instanceType
	if err := SaveManifest(*manifestPath, manifest); err != nil {
		return err
	}
	envPath := *outputEnv
	if envPath == "" {
		envPath = filepath.Join(filepath.Dir(*manifestPath), "ack.env")
	}
	if fileExists(envPath) {
		if err := updateEnvFileValues(envPath, map[string]string{
			"TEST_CAPACITY_RESERVATION_ID":            id,
			"TEST_CAPACITY_RESERVATION_INSTANCE_TYPE": instanceType,
		}); err != nil {
			return err
		}
	}
	fmt.Println(id)
	return nil
}

func updateEnvFileValues(path string, values map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		for key, value := range values {
			prefix := "export " + key + "="
			if strings.HasPrefix(line, prefix) {
				lines[i] = prefix + shellQuote(value)
				seen[key] = true
			}
		}
	}
	insertAt := len(lines)
	if insertAt > 0 && lines[insertAt-1] == "" {
		insertAt--
	}
	var missing []string
	for key := range values {
		if !seen[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	for _, key := range missing {
		line := "export " + key + "=" + shellQuote(values[key])
		lines = append(lines[:insertAt], append([]string{line}, lines[insertAt:]...)...)
		insertAt++
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runTasks(args []string) error {
	fs := flag.NewFlagSet("tasks", flag.ContinueOnError)
	configPath := fs.String("config", "", "deploy config path")
	manifestPath := fs.String("manifest", "", "owner manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if *manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	manifest, err := LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	applyManifestRuntime(cfg, manifest)
	client, err := newCSClient(cfg)
	if err != nil {
		return err
	}
	resp, err := client.DescribeClusterTasks(tea.String(manifest.ClusterID), &cs.DescribeClusterTasksRequest{
		PageNumber: tea.Int32(1),
		PageSize:   tea.Int32(20),
	})
	if err != nil {
		return err
	}
	if resp == nil || resp.Body == nil {
		return nil
	}
	for _, task := range resp.Body.Tasks {
		if task == nil {
			continue
		}
		code, message := "", ""
		if task.Error != nil {
			code = tea.StringValue(task.Error.Code)
			message = tea.StringValue(task.Error.Message)
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n",
			tea.StringValue(task.TaskId),
			tea.StringValue(task.TaskType),
			tea.StringValue(task.State),
			tea.StringValue(task.Created),
			code,
			message,
		)
	}
	return nil
}

func runNodePools(args []string) error {
	fs := flag.NewFlagSet("nodepools", flag.ContinueOnError)
	configPath := fs.String("config", "", "deploy config path")
	manifestPath := fs.String("manifest", "", "owner manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if *manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	manifest, err := LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	applyManifestRuntime(cfg, manifest)
	client, err := newCSClient(cfg)
	if err != nil {
		return err
	}
	resp, err := client.DescribeClusterNodePools(tea.String(manifest.ClusterID), &cs.DescribeClusterNodePoolsRequest{})
	if err != nil {
		return err
	}
	if resp == nil || resp.Body == nil {
		return nil
	}
	for _, nodepool := range resp.Body.Nodepools {
		if nodepool == nil || nodepool.NodepoolInfo == nil {
			continue
		}
		status := nodepool.Status
		state := ""
		var healthy, initial, failed, total int64
		if status != nil {
			state = tea.StringValue(status.State)
			healthy = tea.Int64Value(status.HealthyNodes)
			initial = tea.Int64Value(status.InitialNodes)
			failed = tea.Int64Value(status.FailedNodes)
			total = tea.Int64Value(status.TotalNodes)
		}
		fmt.Printf("%s\t%s\tstate=%s\thealthy=%d\tinitial=%d\tfailed=%d\ttotal=%d\n",
			tea.StringValue(nodepool.NodepoolInfo.NodepoolId),
			tea.StringValue(nodepool.NodepoolInfo.Name),
			state,
			healthy,
			initial,
			failed,
			total,
		)
	}
	return nil
}

func runEnsureNodePool(args []string) error {
	fs := flag.NewFlagSet("ensure-nodepool", flag.ContinueOnError)
	configPath := fs.String("config", "", "deploy config path")
	manifestPath := fs.String("manifest", "", "owner manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if *manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	manifest, err := LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	applyManifestRuntime(cfg, manifest)
	resources, err := DiscoverClusterResources(context.Background(), cfg, manifest.ClusterID)
	if err != nil {
		return err
	}
	return EnsureBootstrapNodePool(context.Background(), cfg, manifest, resources, manifest.KubeconfigPath)
}

func runDeleteNodePool(args []string) error {
	fs := flag.NewFlagSet("delete-nodepool", flag.ContinueOnError)
	configPath := fs.String("config", "", "deploy config path")
	manifestPath := fs.String("manifest", "", "owner manifest path")
	nodepoolID := fs.String("nodepool-id", "", "ACK nodepool id to delete")
	force := fs.Bool("force", false, "force nodepool deletion")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if *manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}
	if strings.TrimSpace(*nodepoolID) == "" {
		return fmt.Errorf("--nodepool-id is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	manifest, err := LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	applyManifestRuntime(cfg, manifest)
	client, err := newCSClient(cfg)
	if err != nil {
		return err
	}
	if _, err := client.DeleteClusterNodepool(tea.String(manifest.ClusterID), tea.String(strings.TrimSpace(*nodepoolID)), &cs.DeleteClusterNodepoolRequest{
		Force: tea.Bool(*force),
	}); err != nil {
		return fmt.Errorf("delete ACK nodepool %s: %w", strings.TrimSpace(*nodepoolID), err)
	}
	return waitForNodePoolDeleted(context.Background(), client, manifest.ClusterID, strings.TrimSpace(*nodepoolID), 30*time.Minute)
}

func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	configPath := fs.String("config", "", "deploy config path")
	clusterName := fs.String("cluster-name", "", "cluster name")
	gitRef := fs.String("git-ref", "", "git ref")
	suite := fs.String("suite", "", "test suite")
	source := fs.String("source", "alibabacloud", "suite source")
	outputEnv := fs.String("output-env", "", "env output path")
	manifestPath := fs.String("manifest", "", "owner manifest path")
	discoverGPU := fs.Bool("discover-gpu", false, "discover an Alibaba Cloud region and zone with GPU stock before creating the cluster")
	dryRun := fs.Bool("dry-run", false, "render and validate without creating cloud resources")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if *outputEnv == "" {
		return fmt.Errorf("--output-env is required")
	}
	if *manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := validateSuiteConfig(cfg, *suite, *discoverGPU); err != nil {
		return err
	}
	resolvedName := *clusterName
	if resolvedName == "" {
		resolvedName = ResolveClusterName(cfg.Cluster.Name)
	}
	resolvedName = ResolveE2EClusterName(resolvedName)

	manifest := newManifest(cfg, resolvedName, *gitRef, *suite, *source, cfg.Kubeconfig)
	if existing, err := LoadManifest(*manifestPath); err == nil && reusableSetupManifestClusterID(existing) != "" {
		manifest = existing
		manifest.Region = cfg.AlibabaCloud.RegionID
		manifest.GitRef = *gitRef
		manifest.Suite = *suite
		manifest.Source = *source
		resolvedName = manifest.ClusterName
	}
	resources := ClusterResources{
		VSwitchIDs: cfg.Cluster.VSwitchIDs,
	}
	if *discoverGPU {
		candidates, err := DiscoverGPUCapacity(context.Background(), cfg, gpuDiscoveryOptions(cfg))
		if err != nil {
			return err
		}
		if err := applyGPUDiscovery(cfg, &resources, candidates); err != nil {
			return err
		}
		if err := ensureGPUDiscoveryNetworkCompatible(context.Background(), cfg, &resources); err != nil {
			return err
		}
		manifest.Region = cfg.AlibabaCloud.RegionID
	}
	if *dryRun {
		manifest.ClusterID = cfg.AlibabaCloud.ClusterID
		rendered, err := renderDefaultFixtures(cfg, manifest, "", resources)
		if err != nil {
			return err
		}
		if err := SaveManifest(*manifestPath, manifest); err != nil {
			return err
		}
		return WriteEnvFile(*outputEnv, envValues(cfg, manifest, *configPath, "", rendered, resources))
	}

	recordedCluster := false
	var clusterID, kubeconfigPath, endpoint string
	if reusableSetupManifestClusterID(manifest) != "" {
		clusterID, kubeconfigPath, endpoint, err = CompleteExistingACKClusterSetup(context.Background(), cfg, manifest)
	} else {
		clusterID, kubeconfigPath, endpoint, err = SetupACKClusterWithProgress(context.Background(), cfg, resolvedName, *gitRef, func(clusterID string) error {
			recordedCluster = true
			return recordACKClusterManifest(manifest, *manifestPath, clusterID, resolvedName, cfg.Kubeconfig)
		})
	}
	if err != nil {
		if clusterID != "" && !recordedCluster {
			_ = recordACKClusterManifest(manifest, *manifestPath, clusterID, resolvedName, cfg.Kubeconfig)
		}
		return err
	}
	if err := recordACKClusterManifest(manifest, *manifestPath, clusterID, resolvedName, kubeconfigPath); err != nil {
		return err
	}
	manifest.KubeconfigPath = kubeconfigPath
	for i := range manifest.Resources {
		if manifest.Resources[i].Type == "ack-cluster" && manifest.Resources[i].ID == clusterID {
			manifest.Resources[i].State = ResourceStatePending
		}
	}
	gpuInstanceTypes := resources.GPUInstanceTypes
	gpuZones := resources.GPUZones
	resources, err = DiscoverClusterResources(context.Background(), cfg, clusterID)
	if err != nil {
		return err
	}
	resources.GPUInstanceTypes = gpuInstanceTypes
	resources.GPUZones = gpuZones
	resources, err = IntersectGPUZonesWithClusterZones(resources)
	if err != nil {
		return err
	}
	if !shouldSkipCapacityReservation() {
		capacityReservationID, capacityReservationInstanceType, err := EnsureCapacityReservation(context.Background(), cfg, manifest, resources)
		if err != nil {
			return err
		}
		resources.CapacityReservationID = capacityReservationID
		resources.CapacityReservationInstanceType = capacityReservationInstanceType
	}
	rendered, err := renderDefaultFixtures(cfg, manifest, endpoint, resources)
	if err != nil {
		return err
	}
	if err := SaveManifest(*manifestPath, manifest); err != nil {
		return err
	}
	return WriteEnvFile(*outputEnv, envValues(cfg, manifest, *configPath, endpoint, rendered, resources))
}

func validateSuiteConfig(cfg *Config, suite string, discoverGPU bool) error {
	if strings.EqualFold(strings.TrimSpace(suite), "ipv6") && !strings.EqualFold(strings.TrimSpace(cfg.Cluster.IPStack), "ipv6") {
		return fmt.Errorf("IPv6 suite requires cluster.ip_stack=ipv6 in deploy config")
	}
	return nil
}

func ResolveE2EClusterName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "manual"
	}
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "_", "-")
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	name = strings.Trim(name, "-")
	const prefix = "karpenter-alibabacloud-e2e-"
	if strings.HasPrefix(name, prefix) {
		return name
	}
	return prefix + name
}

func reusableSetupManifestClusterID(manifest *Manifest) string {
	if manifest == nil || strings.TrimSpace(manifest.ClusterID) == "" {
		return ""
	}
	for _, resource := range manifest.Resources {
		if resource.Type == "ack-cluster" && resource.ID == manifest.ClusterID && resource.State != ResourceStateDeleted {
			return manifest.ClusterID
		}
	}
	return ""
}

func runRenderConfig(args []string) error {
	fs := flag.NewFlagSet("render-config", flag.ContinueOnError)
	configPath := fs.String("config", "", "source deploy config path")
	outputPath := fs.String("output", "", "rendered deploy config path")
	region := fs.String("region", "", "region override")
	clusterName := fs.String("cluster-name", "", "cluster name override")
	kubeconfig := fs.String("kubeconfig", "", "kubeconfig path override")
	kubernetesVersion := fs.String("k8s-version", "", "Kubernetes version override")
	ipStack := fs.String("ip-stack", "", "cluster IP stack override")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if *outputPath == "" {
		return fmt.Errorf("--output is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := applyRuntimeConfig(cfg, RuntimeConfigOverrides{
		Region:            *region,
		ClusterName:       *clusterName,
		Kubeconfig:        *kubeconfig,
		KubernetesVersion: *kubernetesVersion,
		IPStack:           *ipStack,
	}); err != nil {
		return err
	}
	return SaveConfig(*outputPath, cfg)
}

func recordACKClusterManifest(manifest *Manifest, manifestPath, clusterID, clusterName, kubeconfigPath string) error {
	manifest.ClusterID = clusterID
	manifest.ClusterName = clusterName
	manifest.KubeconfigPath = kubeconfigPath
	upsertACKClusterResource(manifest, Resource{
		Type:         "ack-cluster",
		ID:           clusterID,
		Name:         clusterName,
		SupportsTags: true,
		State:        ResourceStatePending,
	})
	return SaveManifest(manifestPath, manifest)
}

func upsertACKClusterResource(manifest *Manifest, resource Resource) {
	upsertManifestResource(manifest, resource)
}

func runCleanup(args []string) error {
	fs := flag.NewFlagSet("cleanup", flag.ContinueOnError)
	configPath := fs.String("config", "", "deploy config path")
	manifestPath := fs.String("manifest", "", "owner manifest path")
	dryRun := fs.Bool("dry-run", false, "validate cleanup inputs without deleting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if *manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	manifest, err := LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	applyManifestRuntime(cfg, manifest)
	if *dryRun {
		return nil
	}
	return CleanupACKCluster(context.Background(), cfg, manifest, *manifestPath)
}

func runSweep(args []string) error {
	fs := flag.NewFlagSet("sweep", flag.ContinueOnError)
	configPath := fs.String("config", "", "deploy config path")
	manifestPath := fs.String("manifest", "", "owner manifest path")
	deleteResources := fs.Bool("delete", false, "delete owned resources before checking for residue")
	var selectorFlags repeatedStringFlag
	fs.Var(&selectorFlags, "selector", "tag selector in key=value form; repeat to require multiple tags")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	if *manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	manifest, err := LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	applyManifestRuntime(cfg, manifest)
	var report SweepReport
	if len(selectorFlags) > 0 {
		selectors, err := parseSweepSelectorFlags(selectorFlags)
		if err != nil {
			return err
		}
		report, err = SweepResourcesBySelectors(context.Background(), cfg, selectors, *deleteResources)
	} else {
		report, err = SweepOwnedResources(context.Background(), cfg, manifest, *deleteResources)
	}
	if err != nil {
		return err
	}
	for _, resource := range report.Resources {
		fmt.Printf("%s\t%s\t%s\n", resource.Type, resource.ID, resource.Status)
	}
	return report.ResidueError()
}

type repeatedStringFlag []string

func (f *repeatedStringFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *repeatedStringFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func applyManifestRuntime(cfg *Config, manifest *Manifest) {
	if cfg == nil || manifest == nil {
		return
	}
	if manifest.Region != "" {
		cfg.AlibabaCloud.RegionID = manifest.Region
	}
	if manifest.KubeconfigPath != "" {
		cfg.Kubeconfig = manifest.KubeconfigPath
	}
}

func runDump(args []string) error {
	fs := flag.NewFlagSet("dump", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "", "owner manifest path")
	outputDir := fs.String("output-dir", "", "dump output directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" || *outputDir == "" {
		return fmt.Errorf("--manifest and --output-dir are required")
	}
	manifest, err := LoadManifest(*manifestPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(*manifestPath)
	if err == nil {
		_ = os.WriteFile(filepath.Join(*outputDir, "manifest.yaml"), data, 0600)
	}
	var summary strings.Builder
	fmt.Fprintf(&summary, "clusterName: %s\n", manifest.ClusterName)
	fmt.Fprintf(&summary, "clusterID: %s\n", manifest.ClusterID)
	fmt.Fprintf(&summary, "region: %s\n", manifest.Region)
	fmt.Fprintf(&summary, "suite: %s\n", manifest.Suite)
	fmt.Fprintf(&summary, "source: %s\n", manifest.Source)
	fmt.Fprintf(&summary, "kubeconfig: %s\n", manifest.KubeconfigPath)
	for _, resource := range manifest.Resources {
		fmt.Fprintf(&summary, "resource: %s/%s state=%s message=%s\n", resource.Type, resource.ID, resource.State, resource.Message)
	}
	return os.WriteFile(filepath.Join(*outputDir, "manifest-summary.txt"), []byte(summary.String()), 0600)
}

func runDiscoverGPU(args []string) error {
	fs := flag.NewFlagSet("discover-gpu", flag.ContinueOnError)
	configPath := fs.String("config", "", "deploy config path")
	format := fs.String("format", "text", "output format: text or env")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	candidates, err := DiscoverGPUCapacity(context.Background(), cfg, gpuDiscoveryOptions(cfg))
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no GPU capacity found for candidate instance types")
	}
	switch *format {
	case "env":
		fmt.Printf("TEST_REGION=%s\n", shellQuote(candidates[0].RegionID))
		fmt.Printf("TEST_GPU_INSTANCE_TYPES=%s\n", shellQuote(candidates[0].InstanceType))
		var zones []string
		for _, candidate := range candidates {
			if candidate.RegionID == candidates[0].RegionID && candidate.InstanceType == candidates[0].InstanceType {
				zones = appendIfNotEmpty(zones, candidate.ZoneID)
			}
		}
		fmt.Printf("TEST_GPU_ZONES=%s\n", shellQuote(strings.Join(zones, ",")))
	case "text":
		for _, candidate := range candidates {
			fmt.Printf("%s\t%s\t%s\n", candidate.RegionID, candidate.ZoneID, candidate.InstanceType)
		}
	default:
		return fmt.Errorf("unsupported --format %q", *format)
	}
	return nil
}

func newManifest(cfg *Config, clusterName, gitRef, suite, source, kubeconfigPath string) *Manifest {
	return &Manifest{
		ClusterName:    clusterName,
		Region:         cfg.AlibabaCloud.RegionID,
		GitRef:         gitRef,
		Suite:          suite,
		Source:         source,
		CreatedAt:      time.Now().UTC(),
		LeaseExpiresAt: time.Now().UTC().Add(6 * time.Hour),
		KubeconfigPath: kubeconfigPath,
		OwnershipTags: map[string]string{
			"testing/type":           "e2e",
			"testing/cluster":        clusterName,
			"karpenter.sh/discovery": clusterName,
		},
	}
}

func envValues(cfg *Config, manifest *Manifest, deployConfigPath, endpoint string, rendered *RenderedFixtures, resources ClusterResources) map[string]string {
	values := map[string]string{
		"KUBECONFIG":            absolutePath(manifest.KubeconfigPath),
		"TEST_DEPLOY_CONFIG":    absolutePath(deployConfigPath),
		"TEST_REGION":           cfg.AlibabaCloud.RegionID,
		"TEST_CLUSTER_ID":       manifest.ClusterID,
		"TEST_CLUSTER_NAME":     manifest.ClusterName,
		"TEST_CLUSTER_ENDPOINT": endpoint,
	}
	if rendered != nil {
		values["DEFAULT_NODECLASS"] = absolutePath(rendered.NodeClassPath)
		values["DEFAULT_NODEPOOL"] = absolutePath(rendered.NodePoolPath)
	}
	if len(resources.VSwitchIDs) > 0 {
		values["TEST_VSWITCH_IDS"] = strings.Join(resources.VSwitchIDs, ",")
	}
	if len(resources.Zones) > 0 {
		values["TEST_ZONES"] = strings.Join(resources.Zones, ",")
	}
	if len(resources.SecurityGroupIDs) > 0 {
		values["TEST_SECURITY_GROUP_IDS"] = strings.Join(resources.SecurityGroupIDs, ",")
	}
	if len(resources.GPUInstanceTypes) > 0 {
		values["TEST_GPU_INSTANCE_TYPES"] = strings.Join(resources.GPUInstanceTypes, ",")
	}
	if len(resources.GPUZones) > 0 {
		values["TEST_GPU_ZONES"] = strings.Join(resources.GPUZones, ",")
	}
	if resources.CapacityReservationID != "" {
		values["TEST_CAPACITY_RESERVATION_ID"] = resources.CapacityReservationID
		values["TEST_CAPACITY_RESERVATION_INSTANCE_TYPE"] = firstNonEmpty(resources.CapacityReservationInstanceType, capacityReservationInstanceType(cfg))
	}
	if strings.EqualFold(cfg.Cluster.IPStack, "ipv6") {
		values["TEST_IP_FAMILY"] = "ipv6"
	}
	if resources.RAMRole != "" {
		values["TEST_RAM_ROLE"] = resources.RAMRole
	}
	if imageID := firstNonEmpty(os.Getenv("TEST_IMAGE_ID"), resources.WorkerImageID); imageID != "" {
		values["TEST_IMAGE_ID"] = imageID
	} else {
		imageFamily := os.Getenv("TEST_IMAGE_FAMILY")
		if imageFamily == "" {
			imageFamily = defaultImageFamily()
		}
		values["TEST_IMAGE_FAMILY"] = imageFamily
	}
	return values
}

func gpuDiscoveryOptions(cfg *Config) GPUDiscoveryOptions {
	return GPUDiscoveryOptions{
		Regions:            firstList(envList("TEST_GPU_REGIONS"), cfg.GPUDiscovery.Regions),
		InstanceTypes:      firstList(envList("TEST_GPU_INSTANCE_TYPES"), cfg.GPUDiscovery.InstanceTypes, defaultGPUDiscoveryInstanceTypes()),
		InstanceChargeType: defaultString(cfg.GPUDiscovery.InstanceChargeType, cfg.Cluster.NodePool.InstanceChargeType),
		SystemDiskCategory: defaultString(cfg.GPUDiscovery.SystemDiskCategory, cfg.Cluster.NodePool.SystemDiskCategory),
	}
}

func shouldSkipCapacityReservation() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("TEST_SKIP_CAPACITY_RESERVATION")), "true")
}

func envList(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	return splitList(raw)
}

func splitList(raw string) []string {
	var values []string
	for _, part := range strings.Split(raw, ",") {
		values = appendIfNotEmpty(values, part)
	}
	return values
}

func firstList(lists ...[]string) []string {
	for _, values := range lists {
		if len(uniqueNonEmpty(values)) > 0 {
			return uniqueNonEmpty(values)
		}
	}
	return nil
}

func renderDefaultFixtures(cfg *Config, manifest *Manifest, endpoint string, resources ClusterResources) (*RenderedFixtures, error) {
	outDir := filepath.Join(".e2e", manifest.ClusterName, "fixtures")
	root := repoRoot()
	return RenderFixtures(
		filepath.Join(root, "test", "pkg", "environment", "alibabacloud", "default_ecsnodeclass.yaml"),
		filepath.Join(root, "test", "pkg", "environment", "alibabacloud", "default_nodepool.yaml"),
		outDir,
		FixtureData{
			ClusterID:        manifest.ClusterID,
			ClusterName:      manifest.ClusterName,
			ClusterEndpoint:  endpoint,
			ImageID:          firstNonEmpty(os.Getenv("TEST_IMAGE_ID"), resources.WorkerImageID),
			ImageFamily:      defaultImageFamily(),
			VSwitchIDs:       resources.VSwitchIDs,
			SecurityGroupIDs: resources.SecurityGroupIDs,
			RAMRole:          resources.RAMRole,
		},
	)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func defaultImageFamily() string {
	if imageFamily := os.Getenv("TEST_IMAGE_FAMILY"); imageFamily != "" {
		return imageFamily
	}
	return "acs:alibaba_cloud_linux_3_2104_x64_container_optimized"
}

func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

func absolutePath(path string) string {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
