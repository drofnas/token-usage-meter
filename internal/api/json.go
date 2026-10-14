// Package api serves the prepared public snapshot without importing the collector.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

const MaxBytes = 4096
const maxEpoch = 4102444800
const daySeconds = 86400
const weekSeconds = 7 * daySeconds

var invalid = errors.New("snapshot_invalid")

type document = map[string]any

// Decode tokens ourselves: encoding/json otherwise accepts duplicate keys and
// invalid UTF-8. Keep numbers as numbers until every field has been checked.
func decode(raw []byte) (document, error) {
	return decodeVersion(raw, MaxBytes, 1)
}
func decodeVersion(raw []byte, limit int, version int) (document, error) {
	if len(raw) > limit {
		return nil, errors.New("snapshot_oversized")
	}
	if !utf8.Valid(raw) {
		return nil, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(int) (any, error)
	value = func(depth int) (any, error) {
		if depth > 24 {
			return nil, invalid
		}
		tok, err := d.Token()
		if err != nil {
			return nil, invalid
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				m := document{}
				for d.More() {
					key, err := d.Token()
					k, ok := key.(string)
					if err != nil || !ok {
						return nil, invalid
					}
					if _, exists := m[k]; exists {
						return nil, invalid
					}
					v, err := value(depth + 1)
					if err != nil {
						return nil, err
					}
					m[k] = v
				}
				end, err := d.Token()
				if err != nil || end != json.Delim('}') {
					return nil, invalid
				}
				return m, nil
			case '[':
				a := []any{}
				for d.More() {
					v, err := value(depth + 1)
					if err != nil {
						return nil, err
					}
					a = append(a, v)
				}
				end, err := d.Token()
				if err != nil || end != json.Delim(']') {
					return nil, invalid
				}
				return a, nil
			default:
				return nil, invalid
			}
		case json.Number:
			n, err := t.Float64()
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return nil, invalid
			}
			return n, nil
		default:
			return tok, nil
		}
	}
	v, err := value(0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, invalid
	}
	m, ok := v.(document)
	if !ok {
		return nil, invalid
	}
	if v, exists := m["version"]; exists && v != float64(version) {
		return nil, errors.New("snapshot_version")
	}
	return m, nil
}
func keys(m document, names string) bool {
	required := strings.Fields(names)
	if len(m) != len(required) {
		return false
	}
	for _, k := range required {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}
func number(v any, low, high float64, integral, nullable bool) bool {
	if v == nil {
		return nullable
	}
	n, ok := v.(float64)
	return ok && n >= low && n <= high && (!integral || n == math.Trunc(n))
}
func epoch(v any, nullable bool) bool { return number(v, 1, maxEpoch, true, nullable) }
func oneOf(v any, nullable bool, values ...string) bool {
	if v == nil {
		return nullable
	}
	s, ok := v.(string)
	return ok && slices.Contains(values, s)
}
func hexToken(s string) bool { return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == "" }
