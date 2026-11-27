package tools

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// defaultAutoBackgroundSeconds is microPi's DEFAULT_AUTO_BACKGROUND_SECONDS: how long a command runs in the foreground
// before it goes on in the background. oh-my-pi takes 60 s; models rarely work on meanwhile, so a test run of 72 s cost
// two turns of waiting in harnessbench at 60 s.
const defaultAutoBackgroundSeconds = 120

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

// µPi: microPi's fixed-sleep handling (bash.ts leadingSleep, fixedSleepSeconds, MAX_FIXED_SLEEP_SECONDS).

// maxFixedSleepSeconds: a fixed `sleep` up to this long runs as it is: waiting a moment for a server or a file is fine.
const maxFixedSleepSeconds = 10

var sleepUnitSeconds = map[string]float64{"": 1, "s": 1, "m": 60, "h": 3600, "d": 86400}

var (
	leadingSleepRE = regexp.MustCompile(`(?s)^\s*sleep\s+(\d+(?:\.\d+)?)([smhd]?)[ \t]*(?:;|&&|\n|$)(.*)$`)
	// fixedSleepRE is microPi's /(?:^|[\s;&|('"])sleep\s+(\d+(?:\.\d+)?)([smhd]?)(?=$|[\s;&|)'"])/g without the
	// lookahead, which RE2 lacks: fixedSleepSeconds checks the character after a match.
	fixedSleepRE = regexp.MustCompile(`(?:^|[\s;&|('"])sleep\s+(\d+(?:\.\d+)?)([smhd]?)`)
)

// leadingSleep reports a command that starts with a fixed `sleep`: its seconds and the rest of the command (maybe empty).
func leadingSleep(command string) (seconds float64, rest string, ok bool) {
	m := leadingSleepRE.FindStringSubmatch(command)
	if m == nil {
		return 0, "", false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, "", false
	}
	return n * sleepUnitSeconds[m[2]], strings.TrimSpace(m[3]), true
}

// fixedSleepSeconds is the longest fixed `sleep` in a command, in seconds, outside a loop (a loop waits on a
// condition, which is what to do); 0 when there is none.
func fixedSleepSeconds(command string) float64 {
	// The body of a loop (`do ... done`, innermost first) is left out.
	outside := command
	for {
		next := removeInnermostLoopBody(outside)
		if next == outside {
			break
		}
		outside = next
	}
	longest := 0.0
	for _, m := range fixedSleepRE.FindAllStringSubmatchIndex(outside, -1) {
		if end := m[1]; end < len(outside) && !strings.ContainsRune(" \t\n\f\r\v;&|)'\"", rune(outside[end])) {
			continue
		}
		n, err := strconv.ParseFloat(outside[m[2]:m[3]], 64)
		if err != nil {
			continue
		}
		longest = max(longest, n*sleepUnitSeconds[outside[m[4]:m[5]]])
	}
	return longest
}

// removeInnermostLoopBody replaces the first `do ... done` with no `do` inside it by a space, as microPi's
// /\bdo\b(?:(?!\bdo\b)[\s\S])*?\bdone\b/ does: the leftmost word `do` whose next word `do` or `done` is a `done`.
func removeInnermostLoopBody(command string) string {
	type keyword struct {
		at   int
		done bool
	}
	var words []keyword
	for i := 0; i+2 <= len(command); i++ {
		if command[i] != 'd' || command[i+1] != 'o' || (i > 0 && isJSWordByte(command[i-1])) {
			continue
		}
		switch {
		case i+2 == len(command) || !isJSWordByte(command[i+2]):
			words = append(words, keyword{at: i})
		case strings.HasPrefix(command[i:], "done") && (i+4 == len(command) || !isJSWordByte(command[i+4])):
			words = append(words, keyword{at: i, done: true})
		}
	}
	for k := 0; k+1 < len(words); k++ {
		if !words[k].done && words[k+1].done {
			return command[:words[k].at] + " " + command[words[k+1].at+4:]
		}
	}
	return command
}

// isJSWordByte reports a JavaScript \w character.
func isJSWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// fixedSleepRefusal says why a command with a long fixed sleep was not run, and what to do instead.
func fixedSleepRefusal(seconds, autoBackgroundSeconds float64) string {
	return "Not run: the command sleeps " + jsNumber(seconds) + " seconds, a fixed time longer than a command runs before it goes to the background (" + jsNumber(autoBackgroundSeconds) + " s). To wait for a background job, call wait: it returns when the job ends. " +
		"To wait for something else, wait for the thing itself in a loop, such as `until curl -sf localhost:8000; do sleep 2; done`, " +
		"or `until ssh host 'grep -q EXIT build.log'; do sleep 10; done` for a job on another machine: the loop ends as soon as the condition holds, " +
		"and goes on in the background after a while."
}

// autoBackgroundSeconds is how long a command runs in the foreground before it goes on in the background (microPi's
// DEFAULT_AUTO_BACKGROUND_SECONDS, 120). PI_BASH_BACKGROUND_MS overrides it, for tests.
func autoBackgroundSeconds() float64 {
	if raw := strings.TrimSpace(os.Getenv("PI_BASH_BACKGROUND_MS")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			return float64(ms) / 1000
		}
	}
	return defaultAutoBackgroundSeconds
}
