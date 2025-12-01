// Copyright 2025 Emiliano Spinella (eminwux)
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
//
// SPDX-License-Identifier: Apache-2.0

package ctr

import (
	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
)

// toResources converts CgroupResources to cgroup2.Resources.
func (r CgroupResources) toResources() (*cgroup2.Resources, error) {
	resources := &cgroup2.Resources{}

	if r.CPU != nil && !r.CPU.isZero() {
		cpu, err := r.CPU.toResource()
		if err != nil {
			return nil, err
		}
		resources.CPU = cpu
	}

	if r.Memory != nil && !r.Memory.isZero() {
		resources.Memory = r.Memory.toResource()
	}

	if r.IO != nil && !r.IO.isZero() {
		io, err := r.IO.toResource()
		if err != nil {
			return nil, err
		}
		resources.IO = io
	}

	return resources, nil
}

// isZero checks if CPUResources is zero (all fields are nil/empty).
func (c *CPUResources) isZero() bool {
	if c == nil {
		return true
	}
	return c.Weight == nil &&
		c.Quota == nil &&
		c.Period == nil &&
		c.Cpus == "" &&
		c.Mems == ""
}

// toResource converts CPUResources to cgroup2.CPU.
func (c *CPUResources) toResource() (*cgroup2.CPU, error) {
	if c.Weight != nil {
		if *c.Weight < 1 || *c.Weight > 10000 {
			return nil, ErrInvalidCPUWeight
		}
	}
	cpu := &cgroup2.CPU{
		Weight: c.Weight,
		Cpus:   c.Cpus,
		Mems:   c.Mems,
	}
	if c.Quota != nil || c.Period != nil {
		cpu.Max = cgroup2.NewCPUMax(c.Quota, c.Period)
	}
	return cpu, nil
}

// isZero checks if MemoryResources is zero (all fields are nil).
func (m *MemoryResources) isZero() bool {
	if m == nil {
		return true
	}
	return m.Min == nil &&
		m.Max == nil &&
		m.Low == nil &&
		m.High == nil &&
		m.Swap == nil
}

// toResource converts MemoryResources to cgroup2.Memory.
func (m *MemoryResources) toResource() *cgroup2.Memory {
	return &cgroup2.Memory{
		Min:  m.Min,
		Max:  m.Max,
		Low:  m.Low,
		High: m.High,
		Swap: m.Swap,
	}
}

// isZero checks if IOResources is zero (weight is 0 and no throttles).
func (io *IOResources) isZero() bool {
	if io == nil {
		return true
	}
	return io.Weight == 0 && len(io.Throttle) == 0
}

// toResource converts IOResources to cgroup2.IO.
func (io *IOResources) toResource() (*cgroup2.IO, error) {
	if io.Weight != 0 && (io.Weight < 1 || io.Weight > 1000) {
		return nil, ErrInvalidIOWeight
	}
	maxEntries := make([]cgroup2.Entry, 0, len(io.Throttle))
	for _, entry := range io.Throttle {
		if entry.Type == "" || entry.Rate == 0 || entry.Major < 0 || entry.Minor < 0 {
			return nil, ErrInvalidThrottle
		}
		maxEntries = append(maxEntries, cgroup2.Entry{
			Type:  cgroup2.IOType(entry.Type),
			Major: entry.Major,
			Minor: entry.Minor,
			Rate:  entry.Rate,
		})
	}
	return &cgroup2.IO{
		BFQ: cgroup2.BFQ{Weight: io.Weight},
		Max: maxEntries,
	}, nil
}
