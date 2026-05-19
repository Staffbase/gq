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
	"strconv"
	"strings"
	"time"
)

// resolveTime converts a human-friendly time expression to a Unix timestamp
// string suitable for any Prometheus-compatible API endpoint.
//
// Supported formats:
//   - "now"          → current Unix timestamp
//   - "now-<n><u>"   → current time minus duration  (e.g. "now-1h", "now-30m")
//   - "now+<n><u>"   → current time plus duration   (e.g. "now+5m")
//   - ""             → returned unchanged (caller treats empty as "omit")
//   - anything else  → returned unchanged (already a Unix timestamp or RFC3339)
//
// Duration units: s (seconds), m (minutes), h (hours), d (days), w (weeks).
func resolveTime(s string) (string, error) {
	if s == "" || s == "now" {
		if s == "" {
			return s, nil
		}
		return strconv.FormatInt(time.Now().Unix(), 10), nil
	}

	if after, ok := strings.CutPrefix(s, "now-"); ok {
		d, err := parsePromDuration(after)
		if err != nil {
			return "", fmt.Errorf("invalid relative time %q: %w", s, err)
		}
		return strconv.FormatInt(time.Now().Add(-d).Unix(), 10), nil
	}

	if after, ok := strings.CutPrefix(s, "now+"); ok {
		d, err := parsePromDuration(after)
		if err != nil {
			return "", fmt.Errorf("invalid relative time %q: %w", s, err)
		}
		return strconv.FormatInt(time.Now().Add(d).Unix(), 10), nil
	}

	return s, nil
}

// parsePromDuration parses a bare duration string like "1h", "30m", "7d", "2w".
func parsePromDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("duration too short: %q", s)
	}
	unit := s[len(s)-1]
	value, err := strconv.ParseFloat(s[:len(s)-1], 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid duration value in %q", s)
	}
	switch unit {
	case 's':
		return time.Duration(value * float64(time.Second)), nil
	case 'm':
		return time.Duration(value * float64(time.Minute)), nil
	case 'h':
		return time.Duration(value * float64(time.Hour)), nil
	case 'd':
		return time.Duration(value * float64(24 * time.Hour)), nil
	case 'w':
		return time.Duration(value * float64(7 * 24 * time.Hour)), nil
	default:
		return 0, fmt.Errorf("unknown duration unit %q in %q (use s, m, h, d, w)", string(unit), s)
	}
}
