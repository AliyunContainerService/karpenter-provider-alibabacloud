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

package batcher

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// batchOperations tracks total batch operations by type and status
	batchOperations = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "karpenter",
			Subsystem: "batcher",
			Name:      "operations_total",
			Help:      "Total number of batch operations by type and status",
		},
		[]string{"operation", "status", "reason"},
	)

	// batchSize tracks the distribution of batch sizes
	batchSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "karpenter",
			Subsystem: "batcher",
			Name:      "batch_size",
			Help:      "Distribution of batch sizes",
			Buckets:   []float64{1, 2, 5, 10, 20, 50, 100, 200},
		},
		[]string{"operation"},
	)

	// batchLatency tracks batch processing latency
	batchLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "karpenter",
			Subsystem: "batcher",
			Name:      "latency_seconds",
			Help:      "Batch processing latency in seconds",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"operation", "reason"},
	)

	// windowUtilization tracks how full windows are when flushed
	windowUtilization = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "karpenter",
			Subsystem: "batcher",
			Name:      "window_utilization",
			Help:      "Window utilization percentage when flushed",
			Buckets:   []float64{0.1, 0.2, 0.3, 0.5, 0.7, 0.9, 1.0},
		},
		[]string{"operation", "reason"},
	)

	// apiCallsSaved tracks API calls saved by batching
	apiCallsSaved = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "karpenter",
			Subsystem: "batcher",
			Name:      "api_calls_saved_total",
			Help:      "Total number of API calls saved by batching (batch_size - 1)",
		},
		[]string{"operation"},
	)

	// cacheHits tracks cache hits in deduplication
	cacheHits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "karpenter",
			Subsystem: "batcher",
			Name:      "cache_hits_total",
			Help:      "Total number of cache hits in deduplication",
		},
		[]string{"operation"},
	)

	// cacheMisses tracks cache misses in deduplication
	cacheMisses = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "karpenter",
			Subsystem: "batcher",
			Name:      "cache_misses_total",
			Help:      "Total number of cache misses in deduplication",
		},
		[]string{"operation"},
	)
)

func init() {
	// Register metrics with controller-runtime
	metrics.Registry.MustRegister(
		batchOperations,
		batchSize,
		batchLatency,
		windowUtilization,
		apiCallsSaved,
		cacheHits,
		cacheMisses,
	)
}

// recordBatchMetrics records metrics for a batch operation
func recordBatchMetrics(operation string, size int, reason string, duration time.Duration, err error) {
	status := "success"
	if err != nil {
		status = "error"
	}

	// Record operation count
	batchOperations.WithLabelValues(operation, status, reason).Inc()

	// Record batch size
	batchSize.WithLabelValues(operation).Observe(float64(size))

	// Record latency
	batchLatency.WithLabelValues(operation, reason).Observe(duration.Seconds())

	// Record API calls saved (batch_size - 1)
	if size > 1 {
		apiCallsSaved.WithLabelValues(operation).Add(float64(size - 1))
	}
}

// recordCacheHit records a cache hit
func recordCacheHit(operation string) {
	cacheHits.WithLabelValues(operation).Inc()
}

// recordCacheMiss records a cache miss
func recordCacheMiss(operation string) {
	cacheMisses.WithLabelValues(operation).Inc()
}
