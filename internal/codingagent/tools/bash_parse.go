package tools

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultBashBackgroundAfter = 120 * time.Second
)

var shellBuiltins = map[string]struct{}{
	"cd": {}, "export": {}, "unset": {}, "alias": {}, "unalias": {},
	"source": {}, ".": {}, "eval": {}, "exec": {}, "set": {},
	"shopt": {}, "ulimit": {}, "umask": {}, "wait": {}, "jobs": {},
	"fg": {}, "bg": {}, "kill": {}, "trap": {}, "exit": {},
	"return": {}, "break": {}, "continue": {}, "local": {}, "declare": {},
	"readonly": {}, "typeset": {}, "printf": {}, "echo": {}, "test": {},
	"[": {}, "[[": {}, "true": {}, "false": {}, "pwd": {}, "read": {},
	"command": {}, "builtin": {}, "type": {}, "hash": {}, "shift": {},
	"times": {}, "getopts": {}, "pushd": {}, "popd": {}, "dirs": {},
}

// parseDirectCommand parses a simple single command with no shell syntax so it can be
// executed directly without spawning /bin/bash -c.
func parseDirectCommand(command string) (string, []string, bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" || strings.ContainsAny(trimmed, "\n\r|&;><`$\"*?[]~{}()=#!\\") {
		// Allow single-quoted strings or simple spaces; if any metacharacter is present outside simple tokens, use shell.
		if strings.ContainsAny(trimmed, "\n\r|&;><`$*?[]~{}()=#!") {
			return "", nil, false
		}
	}
	var args []string
	var cur strings.Builder
	inSingle := false
	inDouble := false
	hasToken := false
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if inSingle {
			if c == '\'' {
				inSingle = false
			} else {
				cur.WriteByte(c)
			}
			continue
		}
		if inDouble {
			if c == '"' {
				inDouble = false
			} else if c == '\\' && i+1 < len(trimmed) {
				i++
				cur.WriteByte(trimmed[i])
			} else {
				cur.WriteByte(c)
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
			hasToken = true
		case '"':
			inDouble = true
			hasToken = true
		case '\\':
			if i+1 >= len(trimmed) {
				return "", nil, false
			}
			i++
			cur.WriteByte(trimmed[i])
			hasToken = true
		default:
			if unicode.IsSpace(rune(c)) {
				if hasToken {
					args = append(args, cur.String())
					cur.Reset()
					hasToken = false
				}
			} else {
				cur.WriteByte(c)
				hasToken = true
			}
		}
	}
	if inSingle || inDouble {
		return "", nil, false
	}
	if hasToken {
		args = append(args, cur.String())
	}
	if len(args) == 0 {
		return "", nil, false
	}
	bin := args[0]
	if _, isBuiltin := shellBuiltins[bin]; isBuiltin {
		return "", nil, false
	}
	if strings.Contains(bin, "=") {
		return "", nil, false
	}
	if !strings.Contains(bin, "/") {
		resolved, err := exec.LookPath(bin)
		if err != nil {
			return "", nil, false
		}
		bin = resolved
	}
	return bin, args[1:], true
}

// parseLeadingSleep parses commands starting with `sleep <dur>` optionally followed by `; rest` or `&& rest`.
func parseLeadingSleep(command string) (dur time.Duration, sep string, rest string, ok bool) {
	s := strings.TrimSpace(command)
	if !strings.HasPrefix(s, "sleep") {
		return 0, "", "", false
	}
	if len(s) == len("sleep") || !unicode.IsSpace(rune(s[len("sleep")])) {
		return 0, "", "", false
	}
	afterSleep := strings.TrimSpace(s[len("sleep"):])
	if afterSleep == "" {
		return 0, "", "", false
	}
	// Extract duration token up to whitespace, ;, or &.
	end := 0
	for end < len(afterSleep) {
		c := afterSleep[end]
		if unicode.IsSpace(rune(c)) || c == ';' || c == '&' {
			break
		}
		end++
	}
	tok := afterSleep[:end]
	d, err := parseSleepDuration(tok)
	if err != nil || d <= 0 {
		return 0, "", "", false
	}
	tail := strings.TrimSpace(afterSleep[end:])
	if tail == "" {
		return d, "", "", true
	}
	if strings.HasPrefix(tail, "&&") {
		return d, "&&", strings.TrimSpace(tail[2:]), true
	}
	if strings.HasPrefix(tail, ";") {
		return d, ";", strings.TrimSpace(tail[1:]), true
	}
	return 0, "", "", false
}

func parseSleepDuration(tok string) (time.Duration, error) {
	if tok == "" {
		return 0, strconv.ErrSyntax
	}
	mult := time.Second
	last := tok[len(tok)-1]
	switch last {
	case 's':
		tok = tok[:len(tok)-1]
	case 'm':
		mult = time.Minute
		tok = tok[:len(tok)-1]
	case 'h':
		mult = time.Hour
		tok = tok[:len(tok)-1]
	case 'd':
		mult = 24 * time.Hour
		tok = tok[:len(tok)-1]
	}
	val, err := strconv.ParseFloat(tok, 64)
	if err != nil || val <= 0 {
		return 0, strconv.ErrSyntax
	}
	return time.Duration(val * float64(mult)), nil
}

func backgroundThreshold() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("PI_BASH_BACKGROUND_MS")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return defaultBashBackgroundAfter
}
