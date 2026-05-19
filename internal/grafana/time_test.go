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
	"strconv"
	"testing"
	"time"
)

func TestResolveTime_Empty(t *testing.T) {
	got, err := resolveTime("")
	if err != nil || got != "" {
		t.Errorf("resolveTime(\"\") = %q, %v; want \"\", nil", got, err)
	}
}

func TestResolveTime_Now(t *testing.T) {
	before := time.Now().Unix()
	got, err := resolveTime("now")
	after := time.Now().Unix()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ts, parseErr := strconv.ParseInt(got, 10, 64)
	if parseErr != nil {
		t.Fatalf("result %q is not a Unix timestamp: %v", got, parseErr)
	}
	if ts < before || ts > after {
		t.Errorf("resolveTime(\"now\") = %d, want between %d and %d", ts, before, after)
	}
}

func TestResolveTime_Subtraction(t *testing.T) {
	cases := []struct {
		input    string
		approx   time.Duration // expected offset from now
		tolerance time.Duration
	}{
		{"now-1h", time.Hour, 2 * time.Second},
		{"now-30m", 30 * time.Minute, 2 * time.Second},
		{"now-7d", 7 * 24 * time.Hour, 2 * time.Second},
		{"now-2w", 14 * 24 * time.Hour, 2 * time.Second},
		{"now-90s", 90 * time.Second, 2 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			want := time.Now().Add(-tc.approx)
			got, err := resolveTime(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			ts, err := strconv.ParseInt(got, 10, 64)
			if err != nil {
				t.Fatalf("result %q is not a Unix timestamp: %v", got, err)
			}
			diff := time.Since(time.Unix(ts, 0)) - tc.approx
			if diff < -tc.tolerance || diff > tc.tolerance {
				t.Errorf("resolveTime(%q) = %d, want ~%d (diff %v)", tc.input, ts, want.Unix(), diff)
			}
		})
	}
}

func TestResolveTime_BareDuration(t *testing.T) {
	// "1h" should behave identically to "now-1h"
	before := time.Now().Add(-time.Hour - time.Second)
	got, err := resolveTime("1h")
	after := time.Now().Add(-time.Hour + time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ts, _ := strconv.ParseInt(got, 10, 64)
	if time.Unix(ts, 0).Before(before) || time.Unix(ts, 0).After(after) {
		t.Errorf("resolveTime(\"1h\") = %d, want ~1h ago", ts)
	}
}

func TestResolveTime_Addition(t *testing.T) {
	got, err := resolveTime("now+5m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ts, _ := strconv.ParseInt(got, 10, 64)
	if ts <= time.Now().Unix() {
		t.Errorf("resolveTime(\"now+5m\") should be in the future, got %d", ts)
	}
}

func TestResolveTime_Passthrough(t *testing.T) {
	cases := []string{
		"1621234567",
		"2026-05-19T10:00:00Z",
		"0",
	}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			got, err := resolveTime(s)
			if err != nil || got != s {
				t.Errorf("resolveTime(%q) = %q, %v; want %q, nil", s, got, err, s)
			}
		})
	}
}

func TestResolveTime_InvalidDuration(t *testing.T) {
	cases := []string{"now-", "now-abc", "now-1x", "now-0h"}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			_, err := resolveTime(s)
			if err == nil {
				t.Errorf("resolveTime(%q) should return error", s)
			}
		})
	}
}
