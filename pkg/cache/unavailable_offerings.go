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

package cache

import (
	"fmt"
	"sync"
	"time"
)

// UnavailableOfferingsCache tracks instance type + zone combinations that are temporarily unavailable
type UnavailableOfferingsCache struct {
	mu      sync.RWMutex
	entries map[string]time.Time
	ttl     time.Duration
}

// NewUnavailableOfferingsCache creates a new unavailable offerings cache
func NewUnavailableOfferingsCache() *UnavailableOfferingsCache {
	return &UnavailableOfferingsCache{
		entries: make(map[string]time.Time),
		ttl:     5 * time.Minute, // Default TTL
	}
}

// MarkUnavailable marks an instance type + zone combination as unavailable
func (u *UnavailableOfferingsCache) MarkUnavailable(instanceType, zone, capacityType string) {
	u.mu.Lock()
	defer u.mu.Unlock()

	key := u.makeKey(instanceType, zone, capacityType)
	u.entries[key] = time.Now().Add(u.ttl)
}

// IsAvailable checks if an instance type + zone combination is available
func (u *UnavailableOfferingsCache) IsAvailable(instanceType, zone, capacityType string) bool {
	u.mu.RLock()
	defer u.mu.RUnlock()

	key := u.makeKey(instanceType, zone, capacityType)
	expiryTime, exists := u.entries[key]

	if !exists {
		return true
	}

	// Check if entry has expired
	if time.Now().After(expiryTime) {
		// Entry expired, remove it
		go u.cleanup(key)
		return true
	}

	return false
}

// cleanup removes an expired entry
func (u *UnavailableOfferingsCache) cleanup(key string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.entries, key)
}

// Flush removes all unavailable markings
func (u *UnavailableOfferingsCache) Flush() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.entries = make(map[string]time.Time)
}

// Count returns the number of unavailable offerings
func (u *UnavailableOfferingsCache) Count() int {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return len(u.entries)
}

func (u *UnavailableOfferingsCache) makeKey(instanceType, zone, capacityType string) string {
	return fmt.Sprintf("%s:%s:%s", instanceType, zone, capacityType)
}

// CleanupExpired removes all expired entries
func (u *UnavailableOfferingsCache) CleanupExpired() {
	u.mu.Lock()
	defer u.mu.Unlock()

	now := time.Now()
	for key, expiryTime := range u.entries {
		if now.After(expiryTime) {
			delete(u.entries, key)
		}
	}
}
