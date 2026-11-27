package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// µPi: a file searched a piece at a time has the outcome of the same file searched whole (microPi's searchInPieces):
// the same lines, context and limit, so grep's output does not depend on the file's size.
func TestSearchInPiecesMatchesTheWholeFileSearch(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		switch {
		case i%7 == 0:
			fmt.Fprintf(&b, "line %d HIT\r\n", i)
		case i == 8 || i == 9:
			fmt.Fprintf(&b, "line %d HIT\n", i)
		default:
			fmt.Fprintf(&b, "line %d\n", i)
		}
	}
	b.WriteString("last HIT without a break")
	content := []byte(b.String())
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	matcher, err := buildLineMatcher("HIT", false, true)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		events       []string
		count        int
		limitReached bool
		stopped      bool
	}
	format := func(ev nativeGrepEvent) string {
		return fmt.Sprintf("%d:%v:%q", ev.lineNumber, ev.isMatch, ev.lineText)
	}
	for _, contextLines := range []int{0, 1, 2, 5} {
		for _, limit := range []int{1, 2, 3, 5, 100} {
			for _, already := range []int{0, 1} {
				if already >= limit {
					continue
				}
				opts := nativeGrepOpts{Context: contextLines, Limit: limit}
				var whole, pieces outcome
				whole.count = already
				whole.stopped = searchBufferLines(path, content, matcher, opts, &whole.count, &whole.limitReached, func(ev nativeGrepEvent) bool {
					whole.events = append(whole.events, format(ev))
					return false
				})
				pieces.count = already
				stopped, err := searchInPieces(context.Background(), path, matcher, opts, &pieces.count, &pieces.limitReached, func(ev nativeGrepEvent) bool {
					pieces.events = append(pieces.events, format(ev))
					return false
				})
				if err != nil {
					t.Fatal(err)
				}
				pieces.stopped = stopped
				if fmt.Sprint(whole) != fmt.Sprint(pieces) {
					t.Errorf("context %d, limit %d, %d before:\nwhole:  %v\npieces: %v", contextLines, limit, already, whole, pieces)
				}
			}
		}
	}
}

// A NUL byte in a later piece makes the whole file binary: no matches from the pieces before it.
func TestSearchInPiecesNULInALaterPieceMeansNoMatches(t *testing.T) {
	data := bytes.Repeat([]byte("HIT line\n"), (pieceBytes*2)/9)
	data[pieceBytes+pieceBytes/2] = 0
	path := filepath.Join(t.TempDir(), "late.bin")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	matcher, err := buildLineMatcher("HIT", false, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, contextLines := range []int{0, 2} {
		count, limitReached := 0, false
		emitted := 0
		if _, err := searchInPieces(context.Background(), path, matcher, nativeGrepOpts{Context: contextLines, Limit: 5}, &count, &limitReached, func(nativeGrepEvent) bool {
			emitted++
			return false
		}); err != nil {
			t.Fatal(err)
		}
		if emitted != 0 || count != 0 || limitReached {
			t.Fatalf("context %d: emitted %d, count %d, limit %v; want nothing", contextLines, emitted, count, limitReached)
		}
	}
}
