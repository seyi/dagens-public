// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package services

import (
	"hash/fnv"
)

// PartitionStrategy determines how data is distributed across partitions
type PartitionStrategy interface {
	// GetPartition returns the partition ID for a given key
	GetPartition(key string, numPartitions int) int
}

// HashPartitionStrategy uses hash-based partitioning (default)
type HashPartitionStrategy struct{}

// NewHashPartitionStrategy creates a new hash partition strategy
func NewHashPartitionStrategy() *HashPartitionStrategy {
	return &HashPartitionStrategy{}
}

func (s *HashPartitionStrategy) GetPartition(key string, numPartitions int) int {
	if numPartitions <= 0 {
		return 0
	}

	if numPartitions == 1 {
		return 0
	}

	// Use FNV-1a hash
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() % uint32(numPartitions))
}

// RangePartitionStrategy uses range-based partitioning
// Useful when keys have natural ordering
type RangePartitionStrategy struct {
	ranges []string // Partition boundaries
}

// NewRangePartitionStrategy creates a new range partition strategy
func NewRangePartitionStrategy(ranges []string) *RangePartitionStrategy {
	return &RangePartitionStrategy{
		ranges: ranges,
	}
}

func (s *RangePartitionStrategy) GetPartition(key string, numPartitions int) int {
	// Find first range boundary greater than key
	for i, boundary := range s.ranges {
		if key < boundary {
			return i
		}
	}
	return len(s.ranges)
}

// PartitionInfo contains information about a partition
type PartitionInfo struct {
	ID      int
	Address string
	Healthy bool
	Load    float64 // 0.0 to 1.0
}
