package tools

import (
	"regexp"
	"strings"
)

// IsReadOnlyCommand reports whether a bash command only reads, so it may start while the model's message is still streaming: an early run that is
// then dropped (an abort, a retried stream, other final arguments) must leave nothing behind.
//
// Deliberately narrow: commands from a fixed list, with the options that write or run other programs refused, joined by |, &&, || and ;,
// optionally after cd. No redirections, command or process substitution, backgrounding, variable assignments or line breaks. Anything else runs
// as usual, after the message.
//
// µPi: ports microPi's bash-read-only.ts.
func IsReadOnlyCommand(command string) bool {
	tokens, ok := tokenizeReadOnly(strings.TrimSpace(command))
	if !ok || len(tokens) == 0 {
		return false
	}
	var words []string
	segment := 0
	for _, token := range append(tokens, ";") {
		switch token {
		case "|", ";", "&&", "||":
			if len(words) == 0 || !readsOnly(words, segment == 0) {
				return false
			}
			words = words[:0]
			segment++
		default:
			words = append(words, token)
		}
	}
	return true
}

var (
	tailFollowShort = regexp.MustCompile(`^-[^-]*[fF]`)
	sortOutputShort = regexp.MustCompile(`^-[^-]*o`)
	findWrites      = regexp.MustCompile(`^-(exec|execdir|ok|okdir|delete|fprint|fprint0|fprintf|fls)$`)
)

// readOnlyRefusedOptions are the options that make an otherwise reading command write a file or run another program.
var readOnlyRefusedOptions = map[string]func(arg string) bool{
	"tail": func(a string) bool {
		return a == "-f" || a == "-F" || strings.HasPrefix(a, "--follow") || tailFollowShort.MatchString(a)
	},
	"sort": func(a string) bool {
		return a == "-o" || strings.HasPrefix(a, "--output") || sortOutputShort.MatchString(a) || strings.HasPrefix(a, "--compress-program")
	},
	// date -s (also in a group of short options, -us) and --set set the clock.
	"date": func(a string) bool {
		return strings.HasPrefix(a, "--set") || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "s"))
	},
	// file -C compiles a magic file.
	"file": func(a string) bool { return a == "-C" || a == "--compile" },
	"tree": func(a string) bool { return a == "-o" },
	"rg":   func(a string) bool { return strings.HasPrefix(a, "--pre") },
	"find": func(a string) bool { return findWrites.MatchString(a) },
}

var readOnlyCommands = map[string]struct{}{
	"cat": {}, "head": {}, "tail": {}, "ls": {}, "wc": {}, "pwd": {}, "echo": {}, "stat": {}, "file": {}, "du": {}, "df": {}, "tree": {},
	"realpath": {}, "readlink": {}, "basename": {}, "dirname": {}, "which": {}, "nl": {}, "cut": {}, "sort": {}, "comm": {}, "diff": {},
	"cmp": {}, "md5sum": {}, "sha1sum": {}, "sha256sum": {}, "grep": {}, "egrep": {}, "fgrep": {}, "rg": {}, "find": {}, "jq": {},
	"true": {}, "false": {}, "date": {}, "uname": {}, "whoami": {}, "id": {},
}

// gitReadSubcommands are the git subcommands that only read the repository.
var gitReadSubcommands = map[string]struct{}{
	"status": {}, "log": {}, "diff": {}, "show": {}, "blame": {}, "ls-files": {}, "ls-tree": {}, "rev-parse": {}, "describe": {},
	"shortlog": {}, "grep": {}, "cat-file": {},
}

// unsupportedShellSyntax is what tokenizeReadOnly does not take (with && removed first): line breaks, backquotes, $(, redirections, and a lone &.
var unsupportedShellSyntax = regexp.MustCompile("[\n\r`<>&]|\\$\\(")

// closingQuote returns the index of the quote byte that ends a quoted string starting at from. With escapes set (double quotes) a
// backslash escapes only ", \, $ and `, as in bash; without (ANSI-C $'...') it escapes any byte.
func closingQuote(command string, from int, quote byte, escapes bool) (int, bool) {
	for j := from; j < len(command); j++ {
		switch command[j] {
		case '\\':
			if j+1 < len(command) && (!escapes || strings.IndexByte("\"\\$`", command[j+1]) >= 0) {
				j++
			}
		case quote:
			return j, true
		}
	}
	return 0, false
}

// tokenizeReadOnly splits a command into words and the operators between them; ok is false for what this check does not take.
func tokenizeReadOnly(command string) ([]string, bool) {
	if unsupportedShellSyntax.MatchString(strings.ReplaceAll(command, "&&", "")) {
		return nil, false
	}
	var tokens []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			tokens = append(tokens, word.String())
		}
		word.Reset()
		inWord = false
	}
	for i := 0; i < len(command); i++ {
		switch c := command[i]; c {
		case '\'':
			// Single quotes take no escapes: the quote ends at the next '.
			end := strings.IndexByte(command[i+1:], c)
			if end < 0 {
				return nil, false
			}
			word.WriteString(command[i+1 : i+1+end])
			inWord = true
			i += end + 1
		case '"':
			// In double quotes a backslash escapes ", \, $ and ` (bash): the quote ends where bash ends it, so an escaped " cannot hide a
			// ; or | that bash runs.
			end, ok := closingQuote(command, i+1, '"', true)
			if !ok {
				return nil, false
			}
			word.WriteString(command[i+1 : end])
			inWord = true
			i = end
		case '$':
			if i+1 < len(command) && command[i+1] == '\'' {
				// $'...' (ANSI-C quoting) takes backslash escapes, \' among them.
				end, ok := closingQuote(command, i+2, '\'', false)
				if !ok {
					return nil, false
				}
				word.WriteString(command[i+2 : end])
				inWord = true
				i = end
				continue
			}
			word.WriteByte(c)
			inWord = true
		case '#':
			if !inWord {
				// A comment: what follows is not run, but this check does not read it as the shell does.
				return nil, false
			}
			word.WriteByte(c)
		case ' ', '\t':
			endWord()
		case '|', ';', '&':
			endWord()
			if two := command[i:min(i+2, len(command))]; two == "||" || two == "&&" {
				tokens = append(tokens, two)
				i++
			} else {
				tokens = append(tokens, string(c))
			}
		case '\\':
			if i+1 < len(command) {
				word.WriteByte(command[i+1])
			}
			inWord = true
			i++
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	endWord()
	return tokens, true
}

func readsOnly(words []string, first bool) bool {
	name, args := words[0], words[1:]
	if name == "" || strings.Contains(name, "=") {
		return false
	}
	// cd dir && ...: the shell's own folder, gone with it.
	if name == "cd" {
		return first && len(args) <= 1
	}
	if name == "git" {
		i := 0
		for i < len(args) && (args[i] == "--no-pager" || args[i] == "-C") {
			if args[i] == "-C" {
				i += 2
			} else {
				i++
			}
		}
		if i >= len(args) {
			return false
		}
		if _, ok := gitReadSubcommands[args[i]]; !ok {
			return false
		}
		// --output=<file> writes; grep -O opens a pager on the files.
		for _, a := range args[i+1:] {
			if strings.HasPrefix(a, "--output") || a == "-O" || strings.HasPrefix(a, "--open-files") {
				return false
			}
		}
		return true
	}
	if _, ok := readOnlyCommands[name]; !ok {
		return false
	}
	refused := readOnlyRefusedOptions[name]
	if refused == nil {
		return true
	}
	for _, a := range args {
		if refused(a) {
			return false
		}
	}
	return true
}
