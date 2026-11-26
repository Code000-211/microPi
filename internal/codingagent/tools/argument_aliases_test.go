package tools

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRenameArgumentAliases(t *testing.T) {
	in := map[string]any{"file_path": "/tmp/a.go", "other": 1}
	got := RenameArgumentAliases(in, FilePathAlias)
	want := map[string]any{"path": "/tmp/a.go", "other": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	// Original map must not be mutated.
	if _, ok := in["file_path"]; !ok {
		t.Fatal("input map was mutated")
	}

	// Canonical key already present -> alias must not overwrite it.
	both := map[string]any{"file_path": "/tmp/old.go", "path": "/tmp/keep.go"}
	gotBoth := RenameArgumentAliases(both, FilePathAlias)
	if gotBoth["path"] != "/tmp/keep.go" {
		t.Fatalf("canonical key overwritten: %#v", gotBoth)
	}
}

func TestNormalizeToolArguments(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		aliases map[string]string
		check   map[string]any
		absent  []string
	}{
		{
			name:    "read_file_path",
			raw:     `{"file_path":"a.txt"}`,
			aliases: ReadPathAliases,
			check:   map[string]any{"path": "a.txt"},
			absent:  []string{"file_path"},
		},
		{
			name:    "read_absolute_path",
			raw:     `{"absolute_path":"/tmp/b.txt"}`,
			aliases: ReadPathAliases,
			check:   map[string]any{"path": "/tmp/b.txt"},
			absent:  []string{"absolute_path"},
		},
		{
			name:    "grep_aliases",
			raw:     `{"pattern":"foo","-i":true,"-C":2,"head_limit":10,"include":"*.go"}`,
			aliases: GrepAliases,
			check:   map[string]any{"pattern": "foo", "ignoreCase": true, "context": float64(2), "limit": float64(10), "glob": "*.go"},
			absent:  []string{"-i", "-C", "head_limit", "include"},
		},
		{
			name:    "bash_run_in_background",
			raw:     `{"command":"sleep 1","run_in_background":true}`,
			aliases: BashAliases,
			check:   map[string]any{"command": "sleep 1", "background": true},
			absent:  []string{"run_in_background"},
		},
		{
			name:    "bash_is_background",
			raw:     `{"command":"sleep 1","is_background":true}`,
			aliases: BashAliases,
			check:   map[string]any{"command": "sleep 1", "background": true},
			absent:  []string{"is_background"},
		},
		{
			name:    "canonical_preserved",
			raw:     `{"path":"keep.txt","file_path":"drop.txt"}`,
			aliases: ReadPathAliases,
			check:   map[string]any{"path": "keep.txt", "file_path": "drop.txt"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := NormalizeToolArguments(json.RawMessage(tc.raw), tc.aliases)
			var m map[string]any
			if err := json.Unmarshal(out, &m); err != nil {
				t.Fatalf("unmarshal %s: %v", out, err)
			}
			for k, want := range tc.check {
				if m[k] != want {
					t.Errorf("key %q = %#v, want %#v", k, m[k], want)
				}
			}
			for _, k := range tc.absent {
				if _, ok := m[k]; ok {
					t.Errorf("alias key %q should have been removed: %s", k, out)
				}
			}
		})
	}
}
