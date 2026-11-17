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
	"strings"
	"testing"
)

func testRegistryInstances(t *testing.T) Instances {
	t.Helper()
	reg, err := LoadRegistry(writeConfig(t, twoInstances))
	if err != nil {
		t.Fatal(err)
	}
	return RegistryInstances(reg)
}

func TestRegistryInstances_ReturnTheSameClientPerInstance(t *testing.T) {
	in := testRegistryInstances(t)
	first, err := in.Get("prod")
	if err != nil {
		t.Fatal(err)
	}
	second, err := in.Get("prod")
	if err != nil {
		t.Fatal(err)
	}
	// Identity matters: a fresh client per call would throw away a token that
	// token_command just refreshed, so every call would pay the 401 again.
	if first != second {
		t.Error("expected the cached client to be reused across calls")
	}
	other, err := in.Get("dev")
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Error("expected a distinct client per instance")
	}
}

func TestRegistryInstances_MissingInstanceListsTheOptions(t *testing.T) {
	_, err := testRegistryInstances(t).Get("")
	if err == nil || !strings.Contains(err.Error(), "dev, prod") {
		t.Fatalf("want an error listing dev and prod, got %v", err)
	}
}

func TestRegistryInstances_UnknownInstanceFails(t *testing.T) {
	if _, err := testRegistryInstances(t).Get("staging"); err == nil {
		t.Fatal("expected an error for an unknown instance")
	}
}

func TestRegistryInstances_AreNamed(t *testing.T) {
	in := testRegistryInstances(t)
	if !in.Named() || strings.Join(in.Names, ",") != "dev,prod" {
		t.Errorf("want named instances dev and prod, got %v", in.Names)
	}
}

func TestSingleInstance_IsOneUnnamedInstance(t *testing.T) {
	c := &Client{}
	in := SingleInstance(c)
	if in.Named() || len(in.Names) != 1 || in.Names[0] != "" {
		t.Errorf("want one unnamed instance, got %q", in.Names)
	}
	if q, err := in.Get(""); err != nil || q != c {
		t.Errorf("want the client, got %v, %v", q, err)
	}
	if _, err := in.Get("prod"); err == nil {
		t.Error("a named instance must fail in single-instance mode")
	}
}
