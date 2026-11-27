package tools

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// Canonical argument alias maps for Claude Code and Qwen Code compatibility.
var (
	// FilePathAlias maps Claude Code's file_path to path.
	FilePathAlias = map[string]string{"file_path": "path"}
	// ReadPathAliases maps Claude Code's file_path and Qwen Code's absolute_path to path.
	ReadPathAliases = map[string]string{"file_path": "path", "absolute_path": "path"}
	// EditReplacementAliases maps Claude Code's old_string/new_string to oldText/newText.
	EditReplacementAliases = map[string]string{"old_string": "oldText", "new_string": "newText"}
	// EditTopLevelAliases maps top-level edit aliases.
	EditTopLevelAliases = map[string]string{
		"file_path":  "path",
		"old_string": "oldText",
		"new_string": "newText",
	}
	// GrepAliases maps Claude Code and Qwen Code grep flags to canonical grep parameters.
	GrepAliases = map[string]string{
		"-i":         "ignoreCase",
		"-C":         "context",
		"head_limit": "limit",
		"include":    "glob",
	}
	// BashAliases maps Claude Code's run_in_background and Qwen Code's is_background to background.
	BashAliases = map[string]string{
		"run_in_background": "background",
		"is_background":     "background",
	}
)

// RenameArgumentAliases renames alias keys in args to their canonical names without mutating args
// and without overwriting an already-present canonical key.
func RenameArgumentAliases(args map[string]any, aliases map[string]string) map[string]any {
	if args == nil || len(aliases) == 0 {
		return args
	}
	var renamed map[string]any
	for alias, canonical := range aliases {
		val, hasAlias := args[alias]
		if !hasAlias {
			continue
		}
		if _, hasCanonical := args[canonical]; hasCanonical {
			continue
		}
		if renamed == nil {
			renamed = make(map[string]any, len(args))
			for k, v := range args {
				renamed[k] = v
			}
		}
		renamed[canonical] = val
		delete(renamed, alias)
	}
	if renamed == nil {
		return args
	}
	return renamed
}

// NormalizeToolArguments rewrites top-level alias keys in a JSON object payload using orderedjson,
// preserving existing canonical keys and key ordering. Non-objects or payloads with no applicable
// aliases are returned unchanged.
func NormalizeToolArguments(raw json.RawMessage, aliases map[string]string) json.RawMessage {
	if len(raw) == 0 || len(aliases) == 0 || !mayHoldAliasKey(raw, aliases) {
		return raw
	}
	args, err := orderedjson.Parse(raw)
	if err != nil {
		return raw
	}
	if !renameOrderedAliases(args, aliases) {
		return raw
	}
	out, err := args.MarshalJSON()
	if err != nil {
		return raw
	}
	canonical, err := jsonstringify.Canonicalize(out)
	if err != nil {
		return out
	}
	return canonical
}

// mayHoldAliasKey reports whether raw may name one of the aliases as a top-level key, so that decoding it is needed. Without a \u escape a
// key is written as is, in quotes, so a quoted alias must appear in raw. With one, the keys are read with encoding/json into a map whose
// values are skipped, not copied (it matches keys case-insensitively, which can only ask for a decode that finds nothing). Either way a
// large write or edit that names no alias is not decoded and copied on every call.
func mayHoldAliasKey(raw json.RawMessage, aliases map[string]string) bool {
	if !bytes.Contains(raw, []byte(`\u`)) {
		for alias := range aliases {
			if bytes.Contains(raw, []byte(`"`+alias+`"`)) {
				return true
			}
		}
		return false
	}
	var keys map[string]skippedJSONValue
	if err := json.Unmarshal(raw, &keys); err != nil {
		return true
	}
	for key := range keys {
		for alias := range aliases {
			if strings.EqualFold(key, alias) {
				return true
			}
		}
	}
	return false
}

// skippedJSONValue takes any JSON value without keeping it.
type skippedJSONValue struct{}

func (*skippedJSONValue) UnmarshalJSON([]byte) error { return nil }

func renameOrderedAliases(args *orderedjson.Object, aliases map[string]string) bool {
	if args == nil {
		return false
	}
	changed := false
	for alias, canonical := range aliases {
		val, hasAlias := args.Get(alias)
		if !hasAlias {
			continue
		}
		if _, hasCanonical := args.Get(canonical); hasCanonical {
			continue
		}
		args.Set(canonical, val)
		args.Delete(alias)
		changed = true
	}
	return changed
}
