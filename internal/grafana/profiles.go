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
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// DefaultProfilesDatasourceUID is the UID mops provisions for Pyroscope on
// every observatory instance.
const DefaultProfilesDatasourceUID = "pyroscope"

const pyroscopeQuerier = "querier.v1.QuerierService/"

// maxFlamegraphNodes asks Pyroscope for as little pruning as it will allow;
// the server clamps it to its own limit.
const maxFlamegraphNodes = 200000

// ProfileType is one kind of profile Pyroscope holds, e.g. CPU or memory.
// Its ID is what query_profile takes as profile_type.
type ProfileType struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SampleType string `json:"sampleType"`
	SampleUnit string `json:"sampleUnit"`
}

// ProfileTypes lists the profile types Pyroscope holds.
func (c *Client) ProfileTypes() ([]ProfileType, error) {
	raw, err := c.postProfiles("ProfileTypes", map[string]any{})
	if err != nil {
		return nil, err
	}
	var r struct {
		ProfileTypes []struct {
			ID         string `json:"ID"`
			Name       string `json:"name"`
			SampleType string `json:"sampleType"`
			SampleUnit string `json:"sampleUnit"`
		} `json:"profileTypes"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decoding profile types: %w", err)
	}
	types := make([]ProfileType, 0, len(r.ProfileTypes))
	for _, t := range r.ProfileTypes {
		types = append(types, ProfileType(t))
	}
	return types, nil
}

// ProfileLabelValues lists values of a profile label, e.g. service_name.
func (c *Client) ProfileLabelValues(label, start, end string) ([]string, error) {
	body, err := withRange(map[string]any{"name": label}, start, end)
	if err != nil {
		return nil, err
	}
	raw, err := c.postProfiles("LabelValues", body)
	if err != nil {
		return nil, err
	}
	var r struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decoding label values: %w", err)
	}
	if r.Names == nil {
		r.Names = []string{}
	}
	return r.Names, nil
}

// SelectProfile merges all profiles matching selector (label-matcher syntax,
// e.g. `{service_name="backend"}`) and returns the top functions by self cost.
func (c *Client) SelectProfile(profileTypeID, selector, start, end string, top int) (*ProfileSummary, error) {
	body, err := withRange(map[string]any{
		"profileTypeID": profileTypeID,
		"labelSelector": selector,
		"maxNodes":      maxFlamegraphNodes,
	}, start, end)
	if err != nil {
		return nil, err
	}
	raw, err := c.postProfiles("SelectMergeStacktraces", body)
	if err != nil {
		return nil, err
	}
	return summarizeProfile(raw, top)
}

// withRange adds Pyroscope's start/end (Unix milliseconds) to body.
func withRange(body map[string]any, start, end string) (map[string]any, error) {
	for k, v := range map[string]string{"start": start, "end": end} {
		sec, err := resolveSeconds(v)
		if err != nil {
			return nil, err
		}
		if sec == "" {
			continue
		}
		n, _ := strconv.ParseInt(sec, 10, 64)
		body[k] = n * 1000
	}
	return body, nil
}

func (c *Client) postProfiles(method string, body map[string]any) ([]byte, error) {
	uid := c.ProfilesDatasourceUID
	if uid == "" {
		uid = DefaultProfilesDatasourceUID
	}
	payload, _ := json.Marshal(body)
	return c.proxy(uid, "POST", pyroscopeQuerier+method, bytes.NewReader(payload), "application/json")
}

// ProfileSummary is the LLM-friendly view of a merged profile.
type ProfileSummary struct {
	Total        int64         `json:"total"`
	TopFunctions []ProfileFunc `json:"topFunctions"`
}

// ProfileFunc is one function's self cost.
type ProfileFunc struct {
	Name    string  `json:"name"`
	Self    int64   `json:"self"`
	SelfPct float64 `json:"selfPercent"`
}

// summarizeProfile reduces a SelectMergeStacktraces flamegraph to its top
// functions by self cost. A raw flamegraph is far too large to hand to an LLM.
func summarizeProfile(raw []byte, top int) (*ProfileSummary, error) {
	var r struct {
		Flamegraph struct {
			Names  []string    `json:"names"`
			Total  json.Number `json:"total"`
			Levels []struct {
				Values []json.Number `json:"values"`
			} `json:"levels"`
		} `json:"flamegraph"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decoding profile response: %w", err)
	}
	fg := r.Flamegraph
	total, _ := strconv.ParseInt(string(fg.Total), 10, 64)
	if total == 0 {
		return nil, fmt.Errorf("no profile data for this selector and time range")
	}

	// Each node is 4 values: x-offset delta, total, self, name index.
	// Summing self per name is all we need, so offsets are ignored.
	self := map[string]int64{}
	for _, lvl := range fg.Levels {
		for i := 0; i+3 < len(lvl.Values); i += 4 {
			sv, _ := strconv.ParseInt(string(lvl.Values[i+2]), 10, 64)
			ni, _ := strconv.Atoi(string(lvl.Values[i+3]))
			if sv > 0 && ni >= 0 && ni < len(fg.Names) {
				self[fg.Names[ni]] += sv
			}
		}
	}
	sum := &ProfileSummary{Total: total, TopFunctions: []ProfileFunc{}}
	for n, v := range self {
		sum.TopFunctions = append(sum.TopFunctions, ProfileFunc{Name: n, Self: v, SelfPct: float64(v) * 100 / float64(total)})
	}
	sort.Slice(sum.TopFunctions, func(i, j int) bool {
		a, b := sum.TopFunctions[i], sum.TopFunctions[j]
		if a.Self != b.Self {
			return a.Self > b.Self
		}
		return a.Name < b.Name
	})
	if top > 0 && len(sum.TopFunctions) > top {
		sum.TopFunctions = sum.TopFunctions[:top]
	}
	return sum, nil
}
