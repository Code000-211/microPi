package tools

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// µPi: the shell accumulator cleans what it is given as a terminal would show it, also when an escape sequence or a CRLF is split across chunks.
func TestShellOutputAccumulatorCleansAcrossChunks(t *testing.T) {
	a := newShellOutputAccumulator("pi-test")
	for _, chunk := range []string{"\x1b[3", "1mred\x1b[0m\r", "\nprogress 10%\rprogress 100%\n", "bell\x07 done"} {
		if err := a.Append([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	a.Finish()
	snap := a.Snapshot(true)
	if want := "red\nprogress 100%\nbell done"; snap.Content != want {
		t.Fatalf("content = %q, want %q", snap.Content, want)
	}
	if snap.Truncation.Truncated || snap.HeadLines != 0 {
		t.Fatalf("truncation = %+v head %d", snap.Truncation, snap.HeadLines)
	}
}

// µPi: truncated output keeps its first lines before an omission line and the tail, and the full-output file holds the cleaned output.
func TestShellOutputAccumulatorKeepsTheHeadOfTruncatedOutput(t *testing.T) {
	a := newShellOutputAccumulator("pi-test")
	var want strings.Builder
	for i := 1; i <= 5000; i++ {
		line := fmt.Sprintf("\x1b[32mline %04d\x1b[0m\n", i)
		if err := a.Append([]byte(line)); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&want, "line %04d\n", i)
	}
	a.Finish()
	snap := a.Snapshot(true)
	t.Cleanup(func() { _ = os.Remove(snap.FullOutputPath) })
	if snap.HeadLines != shellHeadLines || !strings.HasPrefix(snap.Content, "line 0001\n") || !strings.Contains(snap.Content, "line 0040\n\n[... ") ||
		strings.Contains(snap.Content, "line 0041\n") || !strings.HasSuffix(snap.Content, "line 5000") {
		t.Fatalf("head %d, content %.120q ... %q", snap.HeadLines, snap.Content, snap.Content[max(0, len(snap.Content)-40):])
	}
	if snap.Truncation.OutputLines-snap.HeadLines+snap.HeadLines > DefaultMaxLinesUpstream || len(snap.Content) > shellMaxBytes+64 {
		t.Fatalf("kept %d lines, %d bytes", snap.Truncation.OutputLines, len(snap.Content))
	}
	if err := a.CloseTempFile(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(snap.FullOutputPath)
	if err != nil || string(data) != want.String() {
		t.Fatalf("full output file: %v, %d bytes, want %d cleaned bytes", err, len(data), want.Len())
	}
}
