// Package usage defines the small, credential-free device contract.
package usage

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
)

type Window struct {
	Remaining float64 `json:"remaining_percent"`
	ResetAt   int64   `json:"reset_at"` // Zero means the provider has not started this window.
}

type Provider struct {
	Scope           string  `json:"scope"`
	ObservedAt      int64   `json:"observed_at"`
	SourceError     string  `json:"source_error"`
	FiveHour        *Window `json:"five_hour"`
	Weekly          *Window `json:"weekly"`
	ResetsAvailable *int64  `json:"resets_available"`
}

type Snapshot struct {
	Version    int      `json:"version"`
	UpdatedAt  int64    `json:"updated_at"`
	AsOf       int64    `json:"as_of"`
	StaleAfter int64    `json:"stale_after_seconds"`
	Claude     Provider `json:"claude"`
	Codex      Provider `json:"codex"`
}

func ValidError(s string) bool {
	switch s {
	case "", "auth_missing", "auth_failed", "source_timeout", "source_unavailable", "source_invalid", "rate_limited":
		return true
	}
	return false
}

func (p Provider) Valid() bool {
	if !ValidError(p.SourceError) || (p.ResetsAvailable != nil && (*p.ResetsAvailable < 0 || *p.ResetsAvailable > math.MaxInt32)) {
		return false
	}
	if p.ObservedAt == 0 {
		return p.Scope == "" && p.FiveHour == nil && p.Weekly == nil && p.ResetsAvailable == nil
	}
	if !ValidEpoch(p.ObservedAt) || len(p.Scope) != 64 || strings.Trim(p.Scope, "0123456789abcdef") != "" {
		return false
	}
	for _, w := range []*Window{p.FiveHour, p.Weekly} {
		if w != nil && (math.IsNaN(w.Remaining) || math.IsInf(w.Remaining, 0) || w.Remaining < 0 || w.Remaining > 100 || (w.ResetAt != 0 && !ValidEpoch(w.ResetAt))) {
			return false
		}
	}
	return true
}

func (s Snapshot) Valid() bool {
	return s.Version == 3 && ValidEpoch(s.UpdatedAt) && ValidEpoch(s.AsOf) && s.StaleAfter >= 30 && s.StaleAfter <= 3600 &&
		s.Claude.Valid() && s.Codex.Valid() && s.Claude.ObservedAt <= s.UpdatedAt && s.Codex.ObservedAt <= s.UpdatedAt
}

func (p Provider) Status(now, stale int64) string {
	if p.ObservedAt == 0 {
		return "unavailable"
	}
	if p.SourceError != "" || now < p.ObservedAt || now-p.ObservedAt >= stale {
		return "stale"
	}
	return "ok"
}

func Decode(raw []byte) (Snapshot, error) {
	var s Snapshot
	if len(raw) == 0 || len(raw) > MaxBytes || StrictJSON(raw, &s, true) != nil || !s.Valid() {
		return Snapshot{}, ErrInvalid
	}
	// Reject omitted/null scalar fields as well as unknown fields and duplicate keys.
	canonical, _ := json.Marshal(s)
	var actual, expected any
	if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(canonical, &expected) != nil {
		return Snapshot{}, ErrInvalid
	}
	// Snapshots written before reset counters remain valid on upgrade.
	for _, name := range []string{"claude", "codex"} {
		if p, ok := actual.(map[string]any)[name].(map[string]any); ok {
			if _, present := p["resets_available"]; !present {
				p["resets_available"] = nil
			}
		}
	}
	if !reflect.DeepEqual(actual, expected) {
		return Snapshot{}, ErrInvalid
	}
	return s, nil
}
