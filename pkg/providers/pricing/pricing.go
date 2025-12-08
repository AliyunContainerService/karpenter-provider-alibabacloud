/*
Copyright 2024 The Alibaba Cloud Karpenter Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pricing

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Provider handles pricing operations for Alibaba Cloud
type Provider struct {
	region     string
	ecsClient  clients.ECSClient
	cache      map[string]float64
	cacheLock  sync.RWMutex
	lastUpdate time.Time
}

// NewProvider creates a new pricing provider
func NewProvider(region string, ecsClient clients.ECSClient) *Provider {
	return &Provider{
		region:    region,
		ecsClient: ecsClient,
		cache:     make(map[string]float64),
	}
}

// GetPricing gets pricing information for all instance types
func (p *Provider) GetPricing(ctx context.Context) (map[string]float64, error) {
	// Check if cache is still valid (less than 1 hour old)
	if time.Since(p.lastUpdate) < time.Hour {
		p.cacheLock.RLock()
		pricing := make(map[string]float64)
		for k, v := range p.cache {
			pricing[k] = v
		}
		p.cacheLock.RUnlock()
		return pricing, nil
	}

	// Cache is expired, refresh it
	return p.refreshPricing(ctx)
}

// refreshPricing refreshes pricing information from Alibaba Cloud API
func (p *Provider) refreshPricing(ctx context.Context) (map[string]float64, error) {
	logger := log.FromContext(ctx)

	// In a real implementation, you would need to call the DescribePrice API
	// for each instance type and zone combination to get accurate pricing.
	// For demonstration purposes, let's create some sample pricing data
	// but note that this should be replaced with actual API calls in production.

	// The following is a placeholder implementation that should be replaced
	// with real API calls to ecs.DescribePrice for each instance type

	// Example of how to use Alibaba Cloud SDK to get pricing:
	// request := ecs.CreateDescribePriceRequest()
	// request.RegionId = p.region
	// request.ResourceType = "instance"
	// request.InstanceType = "ecs.g6.large"  // This would need to be iterated for all instance types
	// response, err := p.ecsClient.DescribePrice(request)
	// if err != nil {
	//     return nil, fmt.Errorf("failed to describe price: %w", err)
	// }
	// price := response.PriceInfo.Price.Currency + response.PriceInfo.Price.TradePrice

	pricing := map[string]float64{
		"ecs.g6.large":  0.12,
		"ecs.g6.xlarge": 0.24,
		"ecs.c6.large":  0.09,
		"ecs.c6.xlarge": 0.18,
	}

	// Update cache
	p.cacheLock.Lock()
	p.cache = pricing
	p.lastUpdate = time.Now()
	p.cacheLock.Unlock()

	logger.Info("refreshed pricing information (placeholder implementation)")
	return pricing, nil
}

// GetInstanceTypePrice gets the price for a specific instance type
func (p *Provider) GetInstanceTypePrice(ctx context.Context, instanceType string) (float64, error) {
	pricing, err := p.GetPricing(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get pricing: %w", err)
	}

	price, ok := pricing[instanceType]
	if !ok {
		// Return a default price if not found
		return 0.0, nil
	}

	return price, nil
}

// GetSpotPricing gets spot pricing information
func (p *Provider) GetSpotPricing(ctx context.Context) (map[string]float64, error) {
	// In a real implementation, you would get spot pricing from the Alibaba Cloud API
	// For now, we'll just return a subset of the regular pricing with a discount

	pricing, err := p.GetPricing(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get pricing: %w", err)
	}

	spotPricing := make(map[string]float64)
	for instanceType, price := range pricing {
		// Apply a 20% discount for spot instances
		spotPricing[instanceType] = price * 0.8
	}

	return spotPricing, nil
}
