package tools

import (
	"encoding/json"
	"testing"
)

// The scan that skips decoding never misses an alias: an alias key spelled with a \u escape is still renamed, and arguments without one are
// returned as they are, also when their values hold \u escapes.
func TestNormalizeToolArgumentsScanFindsEveryAlias(t *testing.T) {
	cases := []struct {
		raw, want string
	}{
		{`{"file_path":"a.go","content":"x"}`, `{"path":"a.go","content":"x"}`},
		{`{"file_path":"a.go","content":"x"}`, `{"path":"a.go","content":"x"}`},
		{`{"path":"a.go","content":"<html>"}`, `{"path":"a.go","content":"<html>"}`},
		{`{"path":"a.go","content":"mentions \"file_path\" in text"}`, `{"path":"a.go","content":"mentions \"file_path\" in text"}`},
	}
	for _, c := range cases {
		got := NormalizeToolArguments(json.RawMessage(c.raw), FilePathAlias)
		var gotValue, wantValue any
		_ = json.Unmarshal(got, &gotValue)
		_ = json.Unmarshal([]byte(c.want), &wantValue)
		if gotJSON, _ := json.Marshal(gotValue); string(gotJSON) != aliasScanJSON(t, wantValue) {
			t.Errorf("NormalizeToolArguments(%s) = %s, want %s", c.raw, got, c.want)
		}
	}
	unchanged := json.RawMessage(`{"path":"a.go","content":"<big>"}`)
	if got := NormalizeToolArguments(unchanged, FilePathAlias); &got[0] != &unchanged[0] {
		t.Error("arguments without an alias were copied")
	}
}

func aliasScanJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
