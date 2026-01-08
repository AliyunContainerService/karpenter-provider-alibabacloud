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

package instancetype

// GPUInstanceType represents GPU memory configuration for instance types
type GPUInstanceType struct {
	// GPUMemory is GPU memory size in MiB (Mebibytes)
	GPUMemory int64
}

// GPUInstanceTypeFamily maps instance family names to GPU memory per GPU
// Unit: MiB (Mebibytes)
// Each entry represents the memory per single GPU in that instance family
var GPUInstanceTypeFamily = map[string]GPUInstanceType{
	"ecs.gn7e": {
		GPUMemory: 81251,
	},
	"ecs.ebmgn7e": {
		GPUMemory: 81251,
	},
	"ecs.gn7": {
		GPUMemory: 40537,
	},
	"ecs.ebmgn7": {
		GPUMemory: 40537,
	},
	"ecs.gn7i": {
		GPUMemory: 22731,
	},
	"ecs.ebmgn7i": {
		GPUMemory: 22731,
	},
	"ecs.gn6i": {
		GPUMemory: 15109,
	},
	"ecs.ebmgn6i": {
		GPUMemory: 15109,
	},
	"ecs.ebmgn6ia": {
		GPUMemory: 15109,
	},
	"ecs.gn6e": {
		GPUMemory: 32510,
	},
	"ecs.ebmgn6e": {
		GPUMemory: 32510,
	},
	"ecs.ebmgn6t": {
		GPUMemory: 11019,
	},
	"ecs.ebmgn7t": {
		GPUMemory: 24245,
	},
	"ecs.gn6v": {
		GPUMemory: 16160,
	},
	"ecs.ebmgn6v": {
		GPUMemory: 16160,
	},
	"ecs.gn5": {
		GPUMemory: 16280,
	},
	"ecs.gn5i": {
		GPUMemory: 7611,
	},
	"ecs.gn4": {
		GPUMemory: 12215,
	},
}

// GPUInstanceTypes maps specific instance type names to their total GPU memory
// Unit: MiB (Mebibytes)
// Each entry represents the total memory for all GPUs in that specific instance type
var GPUInstanceTypes = map[string]GPUInstanceType{
	// gn7e
	"ecs.gn7e-c16g1.4xlarge": {
		GPUMemory: 81251,
	},
	"ecs.gn7e-c16g1.16xlarge": {
		GPUMemory: 325004,
	},
	"ecs.gn7e-c16g1.32xlarge": {
		GPUMemory: 650008,
	},
	// gn7
	"ecs.gn7-c12g1.3xlarge": {
		GPUMemory: 40537,
	},
	"ecs.gn7-c13g1.13xlarge": {
		GPUMemory: 162148,
	},
	"ecs.gn7-c13g1.26xlarge": {
		GPUMemory: 324296,
	},
	// gn7i
	"ecs.gn7i-c8g1.2xlarge": {
		GPUMemory: 22731,
	},
	"ecs.gn7i-c16g1.4xlarge": {
		GPUMemory: 22731,
	},
	"ecs.gn7i-c32g1.8xlarge": {
		GPUMemory: 22731,
	},
	"ecs.gn7i-c32g1.16xlarge": {
		GPUMemory: 45462,
	},
	"ecs.gn7i-c32g1.32xlarge": {
		GPUMemory: 90924,
	},
	"ecs.gn7i-c48g1.12xlarge": {
		GPUMemory: 22731,
	},
	"ecs.gn7i-c56g1.14xlarge": {
		GPUMemory: 22731,
	},
	// vgn6i (deprecated)
	"ecs.vgn6i-m4.xlarge": {
		GPUMemory: 3805,
	},
	"ecs.vgn6i-m8.2xlarge": {
		GPUMemory: 7611,
	},
	// gn6i
	"ecs.gn6i-c4g1.xlarge": {
		GPUMemory: 15109,
	},
	"ecs.gn6i-c40g1.10xlarge": {
		GPUMemory: 15109,
	},
	"ecs.gn6i-c8g1.2xlarge": {
		GPUMemory: 15109,
	},
	"ecs.gn6i-c16g1.4xlarge": {
		GPUMemory: 15109,
	},
	"ecs.gn6i-c24g1.6xlarge": {
		GPUMemory: 15109,
	},
	"ecs.gn6i-c24g1.12xlarge": {
		GPUMemory: 15109,
	},
	"ecs.gn6i-c24g1.24xlarge": {
		GPUMemory: 60436,
	},
	// gn6e
	"ecs.gn6e-c12g1.3xlarge": {
		GPUMemory: 32510,
	},
	"ecs.gn6e-c12g1.12xlarge": {
		GPUMemory: 130040,
	},
	"ecs.gn6e-c12g1.24xlarge": {
		GPUMemory: 260080,
	},
	// gn6v
	"ecs.gn6v-c8g1.2xlarge": {
		GPUMemory: 16160,
	},
	"ecs.gn6v-c8g1.8xlarge": {
		GPUMemory: 64640,
	},
	"ecs.gn6v-c8g1.16xlarge": {
		GPUMemory: 129280,
	},
	"ecs.gn6v-cg1.xlarge": {
		GPUMemory: 129280,
	},
	// vgn5i
	"ecs.vgn5i-m1.large": {
		GPUMemory: 951,
	},
	"ecs.vgn5i-m2.xlarge": {
		GPUMemory: 1902,
	},
	"ecs.vgn5i-m4.2xlarge": {
		GPUMemory: 3804,
	},
	"ecs.vgn5i-m8.4xlarge": {
		GPUMemory: 7611,
	},
	// gn5
	"ecs.gn5-c4g1.xlarge": {
		GPUMemory: 16280,
	},
	"ecs.gn5-c8g1.2xlarge": {
		GPUMemory: 16280,
	},
	"ecs.gn5-c4g1.2xlarge": {
		GPUMemory: 32560,
	},
	"ecs.gn5-c8g1.4xlarge": {
		GPUMemory: 32560,
	},
	"ecs.gn5-c28g1.7xlarge": {
		GPUMemory: 16280,
	},
	"ecs.gn5-c8g1.8xlarge": {
		GPUMemory: 65120,
	},
	"ecs.gn5-c28g1.14xlarge": {
		GPUMemory: 32560,
	},
	"ecs.gn5-c8g1.14xlarge": {
		GPUMemory: 130240,
	},
	// gn5i
	"ecs.gn5i-c2g1.large": {
		GPUMemory: 7611,
	},
	"ecs.gn5i-c4g1.xlarge": {
		GPUMemory: 7611,
	},
	"ecs.gn5i-c8g1.2xlarge": {
		GPUMemory: 7611,
	},
	"ecs.gn5i-c16g1.4xlarge": {
		GPUMemory: 7611,
	},
	"ecs.gn5i-c16g1.8xlarge": {
		GPUMemory: 15222,
	},
	"ecs.gn5i-c28g1.14xlarge": {
		GPUMemory: 15222,
	},
	// gn4
	"ecs.gn4-c4g1.xlarge": {
		GPUMemory: 12215,
	},
	"ecs.gn4-c8g1.2xlarge": {
		GPUMemory: 12215,
	},
	"ecs.gn4.8xlarge": {
		GPUMemory: 12215,
	},
	"ecs.gn4-c4g1.2xlarge": {
		GPUMemory: 24430,
	},
	"ecs.gn4-c8g1.4xlarge": {
		GPUMemory: 24430,
	},
	"ecs.gn4.14xlarge": {
		GPUMemory: 24430,
	},
	// ga1 (deprecated)
	"ecs.ga1.xlarge": {
		GPUMemory: 1902,
	},
	"ecs.ga1.2xlarge": {
		GPUMemory: 3804,
	},
	"ecs.ga1.4xlarge": {
		GPUMemory: 7611,
	},
	"ecs.ga1.8xlarge": {
		GPUMemory: 15216,
	},
	"ecs.ga1.14xlarge": {
		GPUMemory: 30432,
	},
	// ebmgn6e
	"ecs.ebmgn6e.24xlarge": {
		GPUMemory: 260080,
	},
	// ebmgn6v
	"ecs.ebmgn6v.24xlarge": {
		GPUMemory: 129280,
	},
	// ebmgn6i
	"ecs.ebmgn6i.24xlarge": {
		GPUMemory: 60436,
	},
	"ecs.ebmgn8is.32xlarge": {
		GPUMemory: 360448,
	},
	"ecs.ebmgn8te.32xlarge": {
		GPUMemory: 385024,
	},
	"ecs.gn8is-2x.8xlarge": {
		GPUMemory: 90112,
	},
	"ecs.ebmgn9t.48xlarge": {
		GPUMemory: 253952,
	},
	"ecs.gn8is-8x.32xlarge": {
		GPUMemory: 360448,
	},
	"ecs.gn8is.4xlarge": {
		GPUMemory: 45056,
	},
	"ecs.gn8ia.8xlarge": {
		GPUMemory: 45056,
	},
}
