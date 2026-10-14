package usage

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestDecodeStrictShape(t *testing.T) {
	raw, err := os.ReadFile("../../contracts/v3/fixtures/normal.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Decode(raw)
	if err != nil || snapshot.Claude.FiveHour.Remaining != 72 {
		t.Fatal(err)
	}
	for _, s := range []string{
		strings.Replace(string(raw), `"scope":`, `"unknown":`, 1),
		strings.Replace(string(raw), `"remaining_percent":72`, `"remaining_percent":null`, 1),
		strings.Replace(string(raw), `"observed_at":1800000000`, `"observed_at":null`, 1),
		strings.Replace(string(raw), `"version":3`, `"version":3,"version":3`, 1),
		strings.Replace(string(raw), `"source_error":""`, `"source_error":"secret"`, 1),
	} {
		if _, err := Decode([]byte(s)); err == nil {
			t.Fatal("accepted invalid wire shape")
		}
	}
}

func TestResetCountContract(t *testing.T) {
	raw, err := os.ReadFile("../../contracts/v3/fixtures/normal.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"null", "0", "2", "2147483647", "-1", "2147483648", "1.5", `"2"`, "true"} {
		changed := strings.Replace(string(raw), `"resets_available":2`, `"resets_available":`+value, 1)
		s, err := Decode([]byte(changed))
		valid := value == "null" || value == "0" || value == "2" || value == "2147483647"
		if (err == nil) != valid {
			t.Fatal("reset validation", value, err)
		}
		if err == nil {
			encoded, _ := json.Marshal(s)
			if _, err := Decode(encoded); err != nil {
				t.Fatal("reset roundtrip", err)
			}
		}
	}
	legacy := strings.ReplaceAll(strings.ReplaceAll(string(raw), `,"resets_available":null`, ""), `,"resets_available":2`, "")
	if s, err := Decode([]byte(legacy)); err != nil || s.Codex.ResetsAvailable != nil {
		t.Fatal("legacy cache", err)
	}
	s, _ := Decode(raw)
	s.Codex.ObservedAt = 0
	s.Codex.Scope = ""
	s.Codex.FiveHour = nil
	s.Codex.Weekly = nil
	if s.Valid() {
		t.Fatal("count without observation")
	}
}
