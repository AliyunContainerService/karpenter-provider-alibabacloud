package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

type FixtureData struct {
	ClusterID        string
	ClusterName      string
	ClusterEndpoint  string
	ImageID          string
	ImageFamily      string
	VSwitchIDs       []string
	SecurityGroupIDs []string
	RAMRole          string
}

type RenderedFixtures struct {
	NodeClassPath string
	NodePoolPath  string
}

func RenderFixtures(nodeClassTemplate, nodePoolTemplate, outputDir string, data FixtureData) (*RenderedFixtures, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, err
	}
	nodeClassPath := filepath.Join(outputDir, "default_ecsnodeclass.yaml")
	nodePoolPath := filepath.Join(outputDir, "default_nodepool.yaml")
	if err := renderTemplateFile(nodeClassTemplate, nodeClassPath, data); err != nil {
		return nil, fmt.Errorf("render nodeclass fixture: %w", err)
	}
	if err := renderTemplateFile(nodePoolTemplate, nodePoolPath, data); err != nil {
		return nil, fmt.Errorf("render nodepool fixture: %w", err)
	}
	return &RenderedFixtures{NodeClassPath: nodeClassPath, NodePoolPath: nodePoolPath}, nil
}

func renderTemplateFile(src, dst string, data FixtureData) error {
	tpl, err := template.New(filepath.Base(src)).Option("missingkey=error").ParseFiles(src)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return tpl.Execute(f, data)
}
