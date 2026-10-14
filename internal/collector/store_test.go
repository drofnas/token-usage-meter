package collector

import (
	"cyd-token-usage-meter/internal/usage"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRestartAndAtomicity(t *testing.T) {
	c := testConfig(t)
	s, err := OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../contracts/v3/fixtures/normal.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := usage.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.publish(snapshot); err != nil {
		t.Fatal(err)
	}
	if other, err := OpenStore(c); err != errLocked {
		if other != nil {
			other.Close()
		}
		t.Fatal("missing writer lock", err)
	}
	s.Close()
	s, err = OpenStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restored := s.load()
	if restored.Claude.FiveHour.Remaining != 72 || restored.Codex.Weekly.Remaining != 64 {
		t.Fatal("lost restart cache")
	}
	if restored.Codex.ResetsAvailable == nil || *restored.Codex.ResetsAvailable != 2 {
		t.Fatal("lost reset count on restart")
	}
	public := filepath.Join(c.DataDir, "usage.json")
	before, _ := os.ReadFile(public)
	s.rename = func(string, string) error { return errors.New("simulated full disk") }
	if s.publish(snapshot) == nil {
		t.Fatal("hidden write failure")
	}
	after, _ := os.ReadFile(public)
	if string(before) != string(after) {
		t.Fatal("partial snapshot replaced good data")
	}
	s.rename = os.Rename
	temporary := filepath.Join(c.DataDir, ".usage.json.tmp")
	target := filepath.Join(c.StateDir, "private")
	if os.WriteFile(target, []byte("unchanged"), 0600) != nil {
		t.Fatal("fixture")
	}
	if os.Symlink(target, temporary) != nil {
		t.Fatal("symlink fixture")
	}
	if s.publish(snapshot) == nil {
		t.Fatal("followed temporary symlink")
	}
	preserved, _ := os.ReadFile(target)
	if string(preserved) != "unchanged" {
		t.Fatal("overwrote private file")
	}
}
