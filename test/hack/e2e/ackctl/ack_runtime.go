package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	vpc "github.com/alibabacloud-go/vpc-20160428/v7/client"
	"gopkg.in/yaml.v3"
)

func SetupACKCluster(ctx context.Context, cfg *Config, clusterName, gitRef string) (clusterID, kubeconfigPath, endpoint string, err error) {
	return SetupACKClusterWithProgress(ctx, cfg, clusterName, gitRef, nil)
}

func CompleteExistingACKClusterSetup(ctx context.Context, cfg *Config, manifest *Manifest) (clusterID, kubeconfigPath, endpoint string, err error) {
	if manifest == nil || strings.TrimSpace(manifest.ClusterID) == "" {
		return "", "", "", fmt.Errorf("manifest cluster id is required")
	}
	if err := EnsureNodeLoginCredential(cfg); err != nil {
		return "", "", "", fmt.Errorf("ensure node login credential: %w", err)
	}
	client, err := newCSClient(cfg)
	if err != nil {
		return "", "", "", err
	}
	clusterID = strings.TrimSpace(manifest.ClusterID)
	if err := waitForClusterRunning(ctx, client, clusterID, 45*time.Minute); err != nil {
		return clusterID, "", "", err
	}
	kubeconfig, err := getKubeconfig(client, clusterID)
	if err != nil {
		return clusterID, "", "", err
	}
	kubeconfigPath = manifest.KubeconfigPath
	if kubeconfigPath == "" {
		kubeconfigPath = cfg.Kubeconfig
	}
	if kubeconfigPath == "" {
		kubeconfigPath = filepath.Join(".e2e", manifest.ClusterName, "kubeconfig")
	}
	if err := os.MkdirAll(filepath.Dir(kubeconfigPath), 0700); err != nil {
		return clusterID, "", "", err
	}
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0600); err != nil {
		return clusterID, "", "", fmt.Errorf("write kubeconfig: %w", err)
	}
	endpoint = kubeconfigEndpoint([]byte(kubeconfig))
	resources, err := DiscoverClusterResources(ctx, cfg, clusterID)
	if err != nil {
		return clusterID, kubeconfigPath, endpoint, err
	}
	if err := EnsureBootstrapNodePool(ctx, cfg, manifest, resources, kubeconfigPath); err != nil {
		return clusterID, kubeconfigPath, endpoint, err
	}
	if err := installController(ctx, cfg, clusterID, endpoint, kubeconfigPath); err != nil {
		return clusterID, kubeconfigPath, endpoint, err
	}
	return clusterID, kubeconfigPath, endpoint, nil
}

func SetupACKClusterWithProgress(ctx context.Context, cfg *Config, clusterName, gitRef string, onCreated func(clusterID string) error) (clusterID, kubeconfigPath, endpoint string, err error) {
	if err := EnsureNodeLoginCredential(cfg); err != nil {
		return "", "", "", fmt.Errorf("ensure node login credential: %w", err)
	}
	client, err := newCSClient(cfg)
	if err != nil {
		return "", "", "", err
	}
	resp, err := client.CreateCluster(BuildCreateClusterRequest(cfg, clusterName, gitRef))
	if err != nil {
		return "", "", "", fmt.Errorf("create ACK cluster: %w", err)
	}
	if resp == nil || resp.Body == nil || resp.Body.ClusterId == nil || *resp.Body.ClusterId == "" {
		return "", "", "", fmt.Errorf("create ACK cluster returned empty cluster_id")
	}
	clusterID = *resp.Body.ClusterId
	if onCreated != nil {
		if err := onCreated(clusterID); err != nil {
			return clusterID, "", "", fmt.Errorf("record ACK cluster manifest: %w", err)
		}
	}

	if err := waitForClusterRunning(ctx, client, clusterID, 45*time.Minute); err != nil {
		return clusterID, "", "", err
	}

	kubeconfig, err := getKubeconfig(client, clusterID)
	if err != nil {
		return clusterID, "", "", err
	}
	kubeconfigPath = cfg.Kubeconfig
	if kubeconfigPath == "" {
		kubeconfigPath = filepath.Join(".e2e", clusterName, "kubeconfig")
	}
	if err := os.MkdirAll(filepath.Dir(kubeconfigPath), 0700); err != nil {
		return clusterID, "", "", err
	}
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0600); err != nil {
		return clusterID, "", "", fmt.Errorf("write kubeconfig: %w", err)
	}
	endpoint = kubeconfigEndpoint([]byte(kubeconfig))
	resources, err := DiscoverClusterResources(ctx, cfg, clusterID)
	if err != nil {
		return clusterID, kubeconfigPath, endpoint, err
	}
	manifest := &Manifest{
		ClusterID:      clusterID,
		ClusterName:    clusterName,
		OwnershipTags:  tagsToMap(buildOwnershipTags(clusterName, gitRef)),
		KubeconfigPath: kubeconfigPath,
	}
	if err := EnsureBootstrapNodePool(ctx, cfg, manifest, resources, kubeconfigPath); err != nil {
		return clusterID, kubeconfigPath, endpoint, err
	}
	if err := installController(ctx, cfg, clusterID, endpoint, kubeconfigPath); err != nil {
		return clusterID, kubeconfigPath, endpoint, err
	}
	return clusterID, kubeconfigPath, endpoint, nil
}

func EnsureBootstrapNodePool(ctx context.Context, cfg *Config, manifest *Manifest, resources ClusterResources, kubeconfigPath string) error {
	if manifest == nil || manifest.ClusterID == "" {
		return fmt.Errorf("manifest cluster id is required to create bootstrap nodepool")
	}
	if err := EnsureNodeLoginCredential(cfg); err != nil {
		return fmt.Errorf("ensure bootstrap node login credential: %w", err)
	}
	req, err := BuildBootstrapNodePoolRequest(cfg, manifest, resources)
	if err != nil {
		return err
	}
	client, err := newCSClient(cfg)
	if err != nil {
		return err
	}
	nodepoolID, err := findNodePoolIDByName(client, manifest.ClusterID, tea.StringValue(req.NodepoolInfo.Name))
	if err != nil {
		return err
	}
	if nodepoolID == "" {
		resp, err := client.CreateClusterNodePool(tea.String(manifest.ClusterID), req)
		if err != nil {
			if !isDuplicateNodePool(err) {
				return fmt.Errorf("create bootstrap nodepool: %w", err)
			}
		}
		if resp != nil && resp.Body != nil && resp.Body.NodepoolId != nil {
			nodepoolID = strings.TrimSpace(*resp.Body.NodepoolId)
		}
		if nodepoolID == "" {
			nodepoolID, err = findNodePoolIDByName(client, manifest.ClusterID, tea.StringValue(req.NodepoolInfo.Name))
			if err != nil {
				return err
			}
		}
	}
	if nodepoolID != "" {
		if err := waitForNodePoolHealthy(ctx, client, manifest.ClusterID, nodepoolID, tea.Int64Value(req.ScalingGroup.DesiredSize), 30*time.Minute); err != nil {
			return err
		}
	}
	if kubeconfigPath != "" {
		if err := waitForKubernetesReadyNodes(ctx, kubeconfigPath, 20*time.Minute); err != nil {
			return fmt.Errorf("wait for bootstrap node readiness: %w", err)
		}
	}
	return nil
}

func findNodePoolIDByName(client *cs.Client, clusterID, name string) (string, error) {
	resp, err := client.DescribeClusterNodePools(tea.String(clusterID), &cs.DescribeClusterNodePoolsRequest{})
	if err != nil {
		return "", fmt.Errorf("describe ACK nodepools: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return "", nil
	}
	for _, nodepool := range resp.Body.Nodepools {
		if nodepool == nil || nodepool.NodepoolInfo == nil {
			continue
		}
		if tea.StringValue(nodepool.NodepoolInfo.Name) == name {
			return strings.TrimSpace(tea.StringValue(nodepool.NodepoolInfo.NodepoolId)), nil
		}
	}
	return "", nil
}

func waitForNodePoolHealthy(ctx context.Context, client *cs.Client, clusterID, nodepoolID string, desired int64, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		resp, err := client.DescribeClusterNodePoolDetail(tea.String(clusterID), tea.String(nodepoolID))
		if err != nil {
			return fmt.Errorf("describe bootstrap nodepool %s: %w", nodepoolID, err)
		}
		status := resp.GetBody().GetStatus()
		if status != nil {
			state := strings.ToLower(strings.TrimSpace(tea.StringValue(status.State)))
			healthy := tea.Int64Value(status.HealthyNodes)
			failed := tea.Int64Value(status.FailedNodes)
			if failed > 0 {
				return fmt.Errorf("bootstrap nodepool %s has %d failed nodes", nodepoolID, failed)
			}
			if state == "failed" {
				return fmt.Errorf("bootstrap nodepool %s entered failed state", nodepoolID)
			}
			if state == "active" && healthy >= desired {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for bootstrap nodepool %s to become healthy: %w", nodepoolID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForNodePoolDeleted(ctx context.Context, client *cs.Client, clusterID, nodepoolID string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		resp, err := client.DescribeClusterNodePools(tea.String(clusterID), &cs.DescribeClusterNodePoolsRequest{})
		if err != nil {
			return fmt.Errorf("describe ACK nodepools: %w", err)
		}
		found := false
		if resp != nil && resp.Body != nil {
			for _, nodepool := range resp.Body.Nodepools {
				if nodepool == nil || nodepool.NodepoolInfo == nil {
					continue
				}
				if strings.TrimSpace(tea.StringValue(nodepool.NodepoolInfo.NodepoolId)) == nodepoolID {
					found = true
					break
				}
			}
		}
		if !found {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for ACK nodepool %s to delete: %w", nodepoolID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForKubernetesReadyNodes(ctx context.Context, kubeconfigPath string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if hasReadyNode(ctx, kubeconfigPath) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for a ready Kubernetes node: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func hasReadyNode(ctx context.Context, kubeconfigPath string) bool {
	cmd := exec.CommandContext(ctx, "kubectl", "get", "nodes", "-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\t\"}{range .status.conditions[*]}{.type}{\"=\"}{.status}{\",\"}{end}{\"\\n\"}{end}")
	cmd.Env = kubeCommandEnv(os.Environ(), kubeconfigPath)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Ready=True") {
			return true
		}
	}
	return false
}

func isDuplicateNodePool(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already exist") ||
		strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "duplicated") ||
		strings.Contains(msg, "duplicate")
}

func CleanupACKCluster(ctx context.Context, cfg *Config, manifest *Manifest, manifestPath string) error {
	client, err := newCSClient(cfg)
	if err != nil {
		return err
	}
	clusterID, err := cleanupClusterID(cfg, manifest)
	if err != nil {
		return err
	}
	if clusterID == "" {
		return nil
	}
	if err := releaseManifestCapacityReservations(client, cfg, manifest); err != nil {
		return err
	}

	retainAll := false
	deleteMode := "delete"
	deleteOptions := []*cs.DeleteClusterRequestDeleteOptions{
		{ResourceType: tea.String("SLB"), DeleteMode: tea.String(deleteMode)},
		{ResourceType: tea.String("ALB"), DeleteMode: tea.String(deleteMode)},
		{ResourceType: tea.String("SLS_Data"), DeleteMode: tea.String(deleteMode)},
		{ResourceType: tea.String("SLS_ControlPlane"), DeleteMode: tea.String(deleteMode)},
		{ResourceType: tea.String("PrivateZone"), DeleteMode: tea.String(deleteMode)},
	}
	_, err = client.DeleteCluster(tea.String(clusterID), &cs.DeleteClusterRequest{
		RetainAllResources: tea.Bool(retainAll),
		DeleteOptions:      deleteOptions,
	})
	if err != nil {
		if isClusterNotFound(err) {
			markACKClusterResource(manifest, clusterID, ResourceStateDeleted, "")
			return SaveManifest(manifestPath, manifest)
		}
		return fmt.Errorf("delete ACK cluster %s: %w", clusterID, err)
	}
	if err := waitForClusterDeleted(ctx, client, clusterID, 60*time.Minute); err != nil {
		markACKClusterResource(manifest, clusterID, ResourceStateFailed, err.Error())
		_ = SaveManifest(manifestPath, manifest)
		return err
	}
	markACKClusterResource(manifest, clusterID, ResourceStateDeleted, "")
	return SaveManifest(manifestPath, manifest)
}

func releaseManifestCapacityReservations(_ *cs.Client, cfg *Config, manifest *Manifest) error {
	ecsClient, err := newECSClient(cfg)
	if err != nil {
		return err
	}
	var releaseErrs []error
	for i := range manifest.Resources {
		resource := &manifest.Resources[i]
		if resource.Type != "capacity-reservation" || resource.State == ResourceStateDeleted {
			continue
		}
		err := releaseCapacityReservation(ecsClient, cfg, resource.ID)
		if err != nil && !isNotFoundOrDependencyError(err) {
			resource.State = ResourceStateFailed
			resource.Message = err.Error()
			releaseErrs = append(releaseErrs, fmt.Errorf("release capacity reservation %s: %w", resource.ID, err))
			continue
		}
		resource.State = ResourceStateDeleted
		resource.Message = ""
	}
	return errors.Join(releaseErrs...)
}

func releaseCapacityReservation(client *ecs.Client, cfg *Config, id string) error {
	_, err := client.ReleaseCapacityReservation(&ecs.ReleaseCapacityReservationRequest{
		RegionId: tea.String(cfg.AlibabaCloud.RegionID),
		DryRun:   tea.Bool(false),
		PrivatePoolOptions: &ecs.ReleaseCapacityReservationRequestPrivatePoolOptions{
			Id: tea.String(id),
		},
	})
	if err != nil && capacityReservationReleased(client, cfg, id) {
		return nil
	}
	if err == nil || !isLimitedEndTimeCapacityReservation(err) {
		return err
	}
	if _, modifyErr := client.ModifyCapacityReservation(&ecs.ModifyCapacityReservationRequest{
		RegionId:    tea.String(cfg.AlibabaCloud.RegionID),
		EndTimeType: tea.String("Unlimited"),
		PrivatePoolOptions: &ecs.ModifyCapacityReservationRequestPrivatePoolOptions{
			Id: tea.String(id),
		},
	}); modifyErr != nil {
		if capacityReservationReleased(client, cfg, id) {
			return nil
		}
		return fmt.Errorf("modify limited capacity reservation endTimeType: %w", modifyErr)
	}
	_, err = client.ReleaseCapacityReservation(&ecs.ReleaseCapacityReservationRequest{
		RegionId: tea.String(cfg.AlibabaCloud.RegionID),
		DryRun:   tea.Bool(false),
		PrivatePoolOptions: &ecs.ReleaseCapacityReservationRequestPrivatePoolOptions{
			Id: tea.String(id),
		},
	})
	if err != nil && capacityReservationReleased(client, cfg, id) {
		return nil
	}
	return err
}

func capacityReservationReleased(client *ecs.Client, cfg *Config, id string) bool {
	resp, err := client.DescribeCapacityReservations(&ecs.DescribeCapacityReservationsRequest{
		RegionId: tea.String(cfg.AlibabaCloud.RegionID),
		Status:   tea.String("All"),
		PrivatePoolOptions: &ecs.DescribeCapacityReservationsRequestPrivatePoolOptions{
			Ids: tea.String(fmt.Sprintf("[\"%s\"]", id)),
		},
	})
	if err != nil || resp == nil || resp.Body == nil || resp.Body.CapacityReservationSet == nil {
		return false
	}
	for _, item := range resp.Body.CapacityReservationSet.CapacityReservationItem {
		if item == nil {
			continue
		}
		if tea.StringValue(item.PrivatePoolOptionsId) == id && strings.EqualFold(tea.StringValue(item.Status), "Released") {
			return true
		}
	}
	return false
}

func isLimitedEndTimeCapacityReservation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Invalid.Action.ReleaseCapacityReservation")
}

func cleanupClusterID(_ *Config, manifest *Manifest) (string, error) {
	if manifest.ClusterID == "" {
		return "", fmt.Errorf("manifest cluster id is required for cleanup")
	}
	for i := range manifest.Resources {
		if manifest.Resources[i].Type == "ack-cluster" && manifest.Resources[i].ID == manifest.ClusterID {
			if manifest.Resources[i].State == ResourceStateDeleted {
				return "", nil
			}
			return manifest.ClusterID, nil
		}
	}
	return "", fmt.Errorf("manifest does not contain owned ack-cluster resource %s", manifest.ClusterID)
}

func markACKClusterResource(manifest *Manifest, clusterID string, state ResourceState, message string) {
	for i := range manifest.Resources {
		if manifest.Resources[i].Type == "ack-cluster" && manifest.Resources[i].ID == clusterID {
			manifest.Resources[i].State = state
			manifest.Resources[i].Message = message
		}
	}
}

func waitForClusterDeleted(ctx context.Context, client *cs.Client, clusterID string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		resp, err := client.DescribeClusterDetail(tea.String(clusterID))
		if isClusterNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("describe ACK cluster %s while waiting for deletion: %w", clusterID, err)
		}
		if resp == nil || resp.Body == nil || resp.Body.State == nil {
			return nil
		}
		if *resp.Body.State == "deleted" {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for ACK cluster %s deletion: %w", clusterID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func isClusterNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "notfound") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "not exist") ||
		strings.Contains(msg, "does not exist")
}

func newCSClient(cfg *Config) (*cs.Client, error) {
	if cfg.AlibabaCloud.AccessKeyID == "" || cfg.AlibabaCloud.AccessKeySecret == "" {
		return nil, fmt.Errorf("AlibabaCloud access key credentials are required")
	}
	client, err := cs.NewClient(&openapi.Config{
		AccessKeyId:     tea.String(cfg.AlibabaCloud.AccessKeyID),
		AccessKeySecret: tea.String(cfg.AlibabaCloud.AccessKeySecret),
		RegionId:        tea.String(cfg.AlibabaCloud.RegionID),
		ConnectTimeout:  tea.Int(30),
		MaxIdleConns:    tea.Int(100),
	})
	if err != nil {
		return nil, fmt.Errorf("create CS client: %w", err)
	}
	return client, nil
}

func newECSClient(cfg *Config) (*ecs.Client, error) {
	if cfg.AlibabaCloud.AccessKeyID == "" || cfg.AlibabaCloud.AccessKeySecret == "" {
		return nil, fmt.Errorf("AlibabaCloud access key credentials are required")
	}
	client, err := ecs.NewClient(&openapi.Config{
		AccessKeyId:     tea.String(cfg.AlibabaCloud.AccessKeyID),
		AccessKeySecret: tea.String(cfg.AlibabaCloud.AccessKeySecret),
		RegionId:        tea.String(cfg.AlibabaCloud.RegionID),
		ConnectTimeout:  tea.Int(30),
		MaxIdleConns:    tea.Int(100),
	})
	if err != nil {
		return nil, fmt.Errorf("create ECS client: %w", err)
	}
	return client, nil
}

func newVPCClient(cfg *Config) (*vpc.Client, error) {
	if cfg.AlibabaCloud.AccessKeyID == "" || cfg.AlibabaCloud.AccessKeySecret == "" {
		return nil, fmt.Errorf("AlibabaCloud access key credentials are required")
	}
	client, err := vpc.NewClient(&openapi.Config{
		AccessKeyId:     tea.String(cfg.AlibabaCloud.AccessKeyID),
		AccessKeySecret: tea.String(cfg.AlibabaCloud.AccessKeySecret),
		RegionId:        tea.String(cfg.AlibabaCloud.RegionID),
		ConnectTimeout:  tea.Int(30),
		MaxIdleConns:    tea.Int(100),
	})
	if err != nil {
		return nil, fmt.Errorf("create VPC client: %w", err)
	}
	return client, nil
}

func waitForClusterRunning(ctx context.Context, client *cs.Client, clusterID string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		resp, err := client.DescribeClusterDetail(tea.String(clusterID))
		if err == nil && resp != nil && resp.Body != nil && resp.Body.State != nil {
			switch *resp.Body.State {
			case "running":
				return nil
			case "failed":
				return fmt.Errorf("ACK cluster %s entered failed state", clusterID)
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for ACK cluster %s to run: %w", clusterID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func getKubeconfig(client *cs.Client, clusterID string) (string, error) {
	resp, err := client.DescribeClusterV2UserKubeconfig(tea.String(clusterID), &cs.DescribeClusterV2UserKubeconfigRequest{
		PrivateIpAddress:         tea.Bool(false),
		TemporaryDurationMinutes: tea.Int64(1440),
	})
	if err != nil {
		return "", fmt.Errorf("describe kubeconfig: %w", err)
	}
	if resp == nil || resp.Body == nil || resp.Body.Config == nil || *resp.Body.Config == "" {
		return "", fmt.Errorf("describe kubeconfig returned empty config")
	}
	return *resp.Body.Config, nil
}

func kubeconfigEndpoint(data []byte) string {
	var kubeconfig struct {
		Clusters []struct {
			Cluster struct {
				Server string `yaml:"server"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
	}
	if err := yaml.Unmarshal(data, &kubeconfig); err != nil {
		return ""
	}
	if len(kubeconfig.Clusters) == 0 {
		return ""
	}
	return kubeconfig.Clusters[0].Cluster.Server
}

func installController(ctx context.Context, cfg *Config, clusterID, endpoint, kubeconfigPath string) error {
	if err := runCommand(ctx, kubeconfigPath, nil, "kubectl", "create", "namespace", "karpenter"); err != nil {
		// Namespace may already exist on reruns; apply the rest idempotently.
		_ = err
	}
	secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: alibabacloud-credentials
  namespace: karpenter
type: Opaque
stringData:
  access_key_id: %q
  access_key_secret: %q
`, cfg.AlibabaCloud.AccessKeyID, cfg.AlibabaCloud.AccessKeySecret)
	if err := runCommand(ctx, kubeconfigPath, []byte(secretYAML), "kubectl", "apply", "-f", "-"); err != nil {
		return fmt.Errorf("apply controller credentials secret: %w", err)
	}
	imagePullSecretName := strings.TrimSpace(os.Getenv("E2E_CONTROLLER_IMAGE_PULL_SECRET_NAME"))
	if imagePullSecretName == "" {
		imagePullSecretName = "e2e-controller-pull"
	}
	if dockerConfigPath := strings.TrimSpace(os.Getenv("E2E_CONTROLLER_IMAGE_PULL_CONFIG")); dockerConfigPath != "" {
		dockerConfig, err := os.ReadFile(dockerConfigPath)
		if err != nil {
			return fmt.Errorf("read controller image pull config: %w", err)
		}
		imagePullSecretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: karpenter
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: %s
`, imagePullSecretName, base64.StdEncoding.EncodeToString(dockerConfig))
		if err := runCommand(ctx, kubeconfigPath, []byte(imagePullSecretYAML), "kubectl", "apply", "-f", "-"); err != nil {
			return fmt.Errorf("apply controller image pull secret: %w", err)
		}
	}

	chartPath := filepath.Join(repoRoot(), "charts", "karpenter")
	crdPath := filepath.Join(chartPath, "crds")
	if err := runCommand(ctx, kubeconfigPath, nil, "kubectl", "apply", "-f", crdPath); err != nil {
		return fmt.Errorf("apply karpenter CRDs: %w", err)
	}
	helmArgs := helmInstallControllerArgs(cfg, clusterID, endpoint, chartPath, imagePullSecretName)
	if err := runCommand(ctx, kubeconfigPath, nil, helmArgs[0], helmArgs[1:]...); err != nil {
		return fmt.Errorf("install karpenter helm chart: %w", err)
	}
	if err := runCommand(ctx, kubeconfigPath, nil,
		"kubectl", "rollout", "status", "deployment/karpenter", "-n", "karpenter", "--timeout=10m",
	); err != nil {
		return fmt.Errorf("wait for karpenter rollout: %w", err)
	}
	return nil
}

func helmInstallControllerArgs(cfg *Config, clusterID, endpoint, chartPath, imagePullSecretName string) []string {
	args := []string{
		"helm", "upgrade", "--install", "karpenter", chartPath,
		"--namespace", "karpenter",
		"--set", "controller.credentialsSecretName=alibabacloud-credentials",
		"--set", "settings.clusterID=" + clusterID,
		"--set", "settings.clusterEndpoint=" + endpoint,
		"--set", "settings.region=" + cfg.AlibabaCloud.RegionID,
		"--set-string", `settings.featureGates=NodeOverlay=true\,NodeRepair=true`,
		"--set", "controller.tolerations[0].key=karpenter-test",
		"--set", "controller.tolerations[0].operator=Exists",
		"--set", "controller.tolerations[0].effect=NoSchedule",
	}
	if repository := strings.TrimSpace(os.Getenv("E2E_CONTROLLER_IMAGE_REPOSITORY")); repository != "" {
		args = append(args, "--set", "controller.image.repository="+repository)
	}
	if tag := strings.TrimSpace(os.Getenv("E2E_CONTROLLER_IMAGE_TAG")); tag != "" {
		args = append(args, "--set", "controller.image.tag="+tag)
	}
	if strings.TrimSpace(os.Getenv("E2E_CONTROLLER_IMAGE_PULL_CONFIG")) != "" {
		args = append(args, "--set", "imagePullSecrets[0].name="+imagePullSecretName)
	}
	return args
}

func runCommand(ctx context.Context, kubeconfigPath string, stdin []byte, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = kubeCommandEnv(os.Environ(), kubeconfigPath)
	if stdin != nil {
		in, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		go func() {
			defer in.Close()
			_, _ = in.Write(stdin)
		}()
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v failed: %w: %s", name, args, err, string(out))
	}
	return nil
}

func kubeCommandEnv(base []string, kubeconfigPath string) []string {
	env := make([]string, 0, len(base)+5)
	for _, item := range base {
		key := item
		if idx := strings.IndexByte(item, '='); idx >= 0 {
			key = item[:idx]
		}
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		default:
			env = append(env, item)
		}
	}
	env = append(env,
		"KUBECONFIG="+kubeconfigPath,
		"HTTP_PROXY=",
		"HTTPS_PROXY=",
		"ALL_PROXY=",
		"NO_PROXY=*",
	)
	return env
}
