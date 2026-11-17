// Copyright 2026 Staffbase GmbH.
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
// SPDX-License-Identifier: Apache-2.0

package grafana

import (
	"fmt"
	"strings"
	"sync"
)

// Instances are the Grafana instances gq can query, by name. A single-instance
// setup (environment variables or a single-instance config file) is one
// instance named "".
type Instances struct {
	Names []string
	Get   func(name string) (Querier, error)
}

// Named reports a registry: instances with names the caller has to pick from.
func (in Instances) Named() bool {
	return len(in.Names) != 1 || in.Names[0] != ""
}

// SingleInstance serves q as the one unnamed instance.
func SingleInstance(q Querier) Instances {
	return Instances{Names: []string{""}, Get: func(name string) (Querier, error) {
		if name != "" {
			return nil, fmt.Errorf("instance %q: the config defines no instances", name)
		}
		return q, nil
	}}
}

// RegistryInstances serves the registry's instances. Each client is created on
// first use and kept, so a token refreshed after a 401 serves later calls too.
func RegistryInstances(reg *Registry) Instances {
	var mu sync.Mutex
	clients := map[string]*Client{}
	names := reg.InstanceNames()
	return Instances{Names: names, Get: func(name string) (Querier, error) {
		if name == "" {
			return nil, fmt.Errorf("instance is required (available: %s)", strings.Join(names, ", "))
		}
		mu.Lock()
		defer mu.Unlock()
		if c, ok := clients[name]; ok {
			return c, nil
		}
		inst, err := reg.ResolvedInstance(name)
		if err != nil {
			return nil, err
		}
		c := NewClientFromInstance(inst)
		clients[name] = c
		return c, nil
	}}
}
