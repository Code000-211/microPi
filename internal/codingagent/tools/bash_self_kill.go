package tools

// µPi: ports microPi's bash-self-kill.ts. Whether a bash command would kill the agent itself: `pkill -f` (and `pgrep -f`
// feeding `kill`) match a pattern against the whole command line of every process, and the agent's own holds its prompt
// when it was given on the command line, as in `upi -p "…"` and in benchmark runs. A task that mentions a file, say
// `analysis.R`, and a model that stops its script with `pkill -f analysis.R` stopped the agent with it. The bash tool
// refuses such a command and says how to stop the process instead.

import (
	"os"
	"regexp"
	"strings"
	"sync"
)

// selfKillValueOptions are the pkill/pgrep options that take a value, which is not the pattern.
var selfKillValueOptions = map[string]bool{
	"-u": true, "-U": true, "-g": true, "-G": true, "-P": true, "-s": true, "-t": true, "-F": true,
	"--signal": true, "--pidfile": true, "--ns": true, "--nslist": true,
}

var (
	killWordRE      = regexp.MustCompile(`\bkill\b|\bpkill\b|\bxargs\b`)
	shortFlagsRE    = regexp.MustCompile(`^-[A-Za-z0-9]+$`)
	signalFlagRE    = regexp.MustCompile(`^-([A-Z]+|\d+)$`)
	numberFlagRE    = regexp.MustCompile(`^-\d+$`)
	capitalsFlagRE  = regexp.MustCompile(`^-[A-Z]+$`)
	ownCommandLine  string
	ownCommandOnce  sync.Once
	ownCommandFound bool
)

// fullMatchKill is a pattern `pkill -f` matches against whole command lines; exact is `-x`.
type fullMatchKill struct {
	pattern string
	exact   bool
}

// fullMatchKillPatterns returns the patterns that `pkill -f`, or `pgrep -f` in a command that also kills, match against
// whole command lines.
func fullMatchKillPatterns(command string) []fullMatchKill {
	var found []fullMatchKill
	kills := killWordRE.MatchString(command)
	for _, pipeline := range ParseShellSegments(command) {
		for _, segment := range pipeline {
			// Also inside `$( … )`, which the parser leaves in its segment (`kill $(pgrep -f x)`).
			words := segment.Words
			at := -1
			for i, w := range words {
				var name string
				if i == 0 {
					name, _ = CommandName(words)
				} else {
					name = w[strings.LastIndexByte(w, '/')+1:]
				}
				if name == "pkill" || (name == "pgrep" && kills) {
					at = i
					break
				}
			}
			if at < 0 {
				continue
			}
			var args []string
			if at == 0 {
				_, args = CommandName(words)
			} else {
				args = words[at+1:]
			}
			full, exact := false, false
			pattern, hasPattern := "", false
			for i := 0; i < len(args); i++ {
				arg := args[i]
				switch {
				case arg == "--full":
					full = true
				case arg == "--exact":
					exact = true
				case selfKillValueOptions[arg]:
					i++
				case shortFlagsRE.MatchString(arg) && !signalFlagRE.MatchString(arg):
					// Combined short flags (-fx, -9f); a number or an all-capitals word is a signal (-9, -KILL).
					if strings.Contains(arg, "f") {
						full = true
					}
					if strings.Contains(arg, "x") {
						exact = true
					}
				case strings.HasPrefix(arg, "-") && !hasPattern && !numberFlagRE.MatchString(arg) && !capitalsFlagRE.MatchString(arg):
					// Another long option: no pattern.
				case !strings.HasPrefix(arg, "-") && !hasPattern:
					pattern, hasPattern = arg, true
				}
			}
			if full && pattern != "" {
				found = append(found, fullMatchKill{pattern: pattern, exact: exact})
			}
		}
	}
	return found
}

// agentCommandLine is this process's command line as pkill -f sees it; "" and false where /proc is not there.
func agentCommandLine() (string, bool) {
	ownCommandOnce.Do(func() {
		data, err := os.ReadFile("/proc/self/cmdline")
		if err != nil {
			return
		}
		var parts []string
		for _, part := range strings.Split(string(data), "\x00") {
			if part != "" {
				parts = append(parts, part)
			}
		}
		ownCommandLine, ownCommandFound = strings.Join(parts, " "), true
	})
	return ownCommandLine, ownCommandFound
}

// selfKillPattern returns the pattern in command that would kill the agent's own process, if one would.
func selfKillPattern(command string) (string, bool) {
	line, ok := agentCommandLine()
	if !ok {
		return "", false
	}
	return selfKillPatternFor(command, line)
}

// selfKillPatternFor returns the pattern in command that would kill the process with command line line, if one would.
// µPi: JavaScript regular expressions there, RE2 here; a pattern RE2 cannot compile is compared as text, as microPi does
// for a pattern JavaScript cannot compile.
func selfKillPatternFor(command, line string) (string, bool) {
	if line == "" {
		return "", false
	}
	for _, kill := range fullMatchKillPatterns(command) {
		expr := kill.pattern
		if kill.exact {
			expr = "^(?:" + kill.pattern + ")$"
		}
		var matches bool
		if re, err := regexp.Compile(expr); err == nil {
			matches = re.MatchString(line)
		} else if kill.exact {
			matches = line == kill.pattern
		} else {
			matches = strings.Contains(line, kill.pattern)
		}
		if matches {
			return kill.pattern, true
		}
	}
	return "", false
}

// selfKillRefusal says why such a command was not run, and what to do instead.
func selfKillRefusal(pattern string) string {
	return "Not run: `-f " + pattern + "` matches the command line of the agent itself (it holds the task's text), so it would end this session. Stop the process by its PID instead (from `pgrep -af '" + pattern + "'`, choosing the right one, or `$!` after starting it), or use a pattern only that process's command line has."
}
