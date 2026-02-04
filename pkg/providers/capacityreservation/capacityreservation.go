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

package capacityreservation

import (
	"context"
	"fmt"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Provider handles capacity reservation operations for Alibaba Cloud
type Provider struct {
	region    string
	ecsClient clients.ECSClient
}

// NewProvider creates a new capacity reservation provider
func NewProvider(region string, ecsClient clients.ECSClient) *Provider {
	return &Provider{
		region:    region,
		ecsClient: ecsClient,
	}
}

// Resolve resolves capacity reservation selectors to actual capacity reservations
func (p *Provider) Resolve(ctx context.Context, selectorTerms []v1alpha1.CapacityReservationSelectorTerm) ([]v1alpha1.CapacityReservation, error) {
	log := log.FromContext(ctx)

	// If no selector terms, return empty list
	if len(selectorTerms) == 0 {
		return []v1alpha1.CapacityReservation{}, nil
	}

	// For each selector term, resolve to actual capacity reservations
	var capacityReservations []v1alpha1.CapacityReservation

	for _, term := range selectorTerms {
		// If ID is specified, resolve by ID
		if term.ID != nil {
			cr, err := p.getByID(ctx, *term.ID)
			if err != nil {
				log.Error(err, "failed to get capacity reservation by ID", "id", *term.ID)
				return nil, fmt.Errorf("failed to get capacity reservation %s: %w", *term.ID, err)
			}
			capacityReservations = append(capacityReservations, cr)
			continue
		}

		// If tags are specified, resolve by tags
		if len(term.Tags) > 0 {
			crs, err := p.getByTags(ctx, term.Tags)
			if err != nil {
				log.Error(err, "failed to get capacity reservations by tags", "tags", term.Tags)
				return nil, fmt.Errorf("failed to get capacity reservations by tags: %w", err)
			}
			capacityReservations = append(capacityReservations, crs...)
			continue
		}
	}

	return capacityReservations, nil
}

// getByID gets a capacity reservation by ID
func (p *Provider) getByID(ctx context.Context, id string) (v1alpha1.CapacityReservation, error) {
	response, err := p.ecsClient.DescribeCapacityReservations(ctx, id, nil)
	if err != nil {
		return v1alpha1.CapacityReservation{}, fmt.Errorf("failed to describe capacity reservation %s: %w", id, err)
	}

	if len(response.Body.CapacityReservationSet.CapacityReservationItem) == 0 {
		return v1alpha1.CapacityReservation{}, fmt.Errorf("capacity reservation %s not found", id)
	}

	cr := response.Body.CapacityReservationSet.CapacityReservationItem[0]
	return v1alpha1.CapacityReservation{
		ID:   *cr.PrivatePoolOptionsId,
		Name: *cr.PrivatePoolOptionsName,
	}, nil
}

// getByTags gets capacity reservations by tags
func (p *Provider) getByTags(ctx context.Context, tags map[string]string) ([]v1alpha1.CapacityReservation, error) {
	response, err := p.ecsClient.DescribeCapacityReservations(ctx, "", tags)
	if err != nil {
		return nil, fmt.Errorf("failed to describe capacity reservations by tags: %w", err)
	}

	var capacityReservations []v1alpha1.CapacityReservation
	for _, cr := range response.Body.CapacityReservationSet.CapacityReservationItem {
		capacityReservations = append(capacityReservations, v1alpha1.CapacityReservation{
			ID:   *cr.PrivatePoolOptionsId,
			Name: *cr.PrivatePoolOptionsName,
		})
	}

	return capacityReservations, nil
}
