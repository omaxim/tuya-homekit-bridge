package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omaxim/tuya-homekit-bridge/tuya"
)

func TestExamplesLoad(t *testing.T) {
	files, _ := filepath.Glob("../../examples/*.json")
	if len(files) == 0 {
		t.Fatal("no examples")
	}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		tmp := filepath.Join(t.TempDir(), "c.json")
		// examples use placeholders; make the key 16 chars so validation passes
		os.WriteFile(tmp, []byte(replaceKey(string(raw))), 0o600)
		if _, err := loadConfig(tmp); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func replaceKey(s string) string {
	const ph = "REPLACE_WITH_LOCAL_KEY"
	out := ""
	for {
		i := indexOf(s, ph)
		if i < 0 {
			return out + s
		}
		out += s[:i] + "0123456789abcdef"
		s = s[i+len(ph):]
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestMatches(t *testing.T) {
	have := tuya.DPS{"1": true, "2": float64(50), "4": "low"}
	if !matches(have, tuya.DPS{"1": true, "2": float64(50)}) {
		t.Error("expected match")
	}
	if matches(have, tuya.DPS{"4": "high"}) {
		t.Error("unexpected match")
	}
}
