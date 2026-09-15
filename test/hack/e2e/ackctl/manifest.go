package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type ResourceState string

const (
	ResourceStatePending ResourceState = "pending"
	ResourceStateDeleted ResourceState = "deleted"
	ResourceStateSkipped ResourceState = "skipped"
	ResourceStateFailed  ResourceState = "failed"
)

type Manifest struct {
	ClusterName    string            `yaml:"clusterName"`
	ClusterID      string            `yaml:"clusterID"`
	Region         string            `yaml:"region"`
	GitRef         string            `yaml:"gitRef,omitempty"`
	Suite          string            `yaml:"suite,omitempty"`
	Source         string            `yaml:"source,omitempty"`
	CreatedAt      time.Time         `yaml:"createdAt"`
	LeaseExpiresAt time.Time         `yaml:"leaseExpiresAt,omitempty"`
	KubeconfigPath string            `yaml:"kubeconfigPath,omitempty"`
	OwnershipTags  map[string]string `yaml:"ownershipTags"`
	Resources      []Resource        `yaml:"resources"`
}

type Resource struct {
	Type         string        `yaml:"type"`
	ID           string        `yaml:"id"`
	Name         string        `yaml:"name,omitempty"`
	SupportsTags bool          `yaml:"supportsTags"`
	State        ResourceState `yaml:"state"`
	Message      string        `yaml:"message,omitempty"`
}

func SaveManifest(path string, manifest *Manifest) error {
	if manifest.CreatedAt.IsZero() {
		manifest.CreatedAt = time.Now().UTC()
	}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	manifest := &Manifest{}
	if err := yaml.Unmarshal(data, manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return manifest, nil
}

func (m *Manifest) RetryableResources() []Resource {
	var retryable []Resource
	for _, r := range m.Resources {
		if r.State == ResourceStatePending || r.State == ResourceStateFailed {
			retryable = append(retryable, r)
		}
	}
	return retryable
}

func upsertManifestResource(manifest *Manifest, resource Resource) {
	for i := range manifest.Resources {
		if manifest.Resources[i].Type == resource.Type && manifest.Resources[i].ID == resource.ID {
			manifest.Resources[i] = resource
			return
		}
	}
	manifest.Resources = append(manifest.Resources, resource)
}
