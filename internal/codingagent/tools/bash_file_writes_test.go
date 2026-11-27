package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/filetracking"
)

func TestFileWritesInCommand(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		wantLen int
		wantHow string
		wantTgt string
	}{
		{
			name:    "sed_in_place",
			cmd:     `sed -i 's/foo/bar/g' src/app.go`,
			wantLen: 1,
			wantHow: "sed -i",
			wantTgt: "src/app.go",
		},
		{
			name:    "perl_in_place",
			cmd:     `perl -pi -e 's/a/b/g' main.go`,
			wantLen: 1,
			wantHow: "perl -i",
			wantTgt: "main.go",
		},
		{
			name:    "echo_redirect",
			cmd:     `echo "hello" > out.txt`,
			wantLen: 1,
			wantHow: "a redirection",
			wantTgt: "out.txt",
		},
		{
			name:    "cat_heredoc_redirect",
			cmd:     "cat <<'EOF' > config.json\n{\"a\":1}\nEOF\n",
			wantLen: 1,
			wantHow: "a redirection",
			wantTgt: "config.json",
		},
		{
			name:    "tee_heredoc",
			cmd:     "cat <<EOF | tee a.txt\nhi\nEOF\n",
			wantLen: 1,
			wantHow: "tee",
			wantTgt: "a.txt",
		},
		{
			name:    "append_ignored",
			cmd:     `echo "log" >> app.log`,
			wantLen: 0,
		},
		{
			name:    "scratch_tmp_ignored",
			cmd:     `echo "tmp" > /tmp/scratch.txt`,
			wantLen: 0,
		},
		{
			name:    "cd_tmp_ignored",
			cmd:     `cd /tmp && echo "x" > test.txt`,
			wantLen: 0,
		},
		{
			name:    "quoted_ssh_ignored",
			cmd:     `ssh host 'sed -i s/a/b/ file.txt'`,
			wantLen: 0,
		},
		{
			name:    "program_output_ignored",
			cmd:     `make > build.log`,
			wantLen: 0,
		},
		{
			name:    "sed_glob_ignored",
			cmd:     `sed -i 's/a/b/' *.txt`,
			wantLen: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FileWritesInCommand(tc.cmd)
			if len(got) != tc.wantLen {
				t.Fatalf("FileWritesInCommand(%q) len = %d (%+v), want %d", tc.cmd, len(got), got, tc.wantLen)
			}
			if tc.wantLen == 1 {
				if got[0].How != tc.wantHow || got[0].Target != tc.wantTgt {
					t.Errorf("got %+v, want How=%q Target=%q", got[0], tc.wantHow, tc.wantTgt)
				}
			}
		})
	}
}

func TestFilesShownByCat(t *testing.T) {
	dir, files := FilesShownByCat("cd sub && cat -n a.go b.go")
	if dir != "sub" || len(files) != 2 || files[0] != "a.go" || files[1] != "b.go" {
		t.Fatalf("got (%q, %+v)", dir, files)
	}
	if _, f := FilesShownByCat("cat a.go | grep foo"); len(f) != 0 {
		t.Fatalf("piped cat should return empty, got %+v", f)
	}
	if _, f := FilesShownByCat("cat -A a.go"); len(f) != 0 {
		t.Fatalf("cat -A should return empty, got %+v", f)
	}
}

func TestFileWriteRefusalAndWatchFiles(t *testing.T) {
	writes := FileWritesInCommand("sed -i 's/a/b/' dir/file.go")
	refusal := FileWriteRefusal(writes)
	// µPi: microPi's fileWriteRefusal wording.
	want := "Not run: the command changes dir/file.go (sed -i). Change files with edit (exact replacements: it checks that the file was read and did not change since, and reports the compiler's new errors) or write (whole files). Appending (`>>`), files under /tmp, and a program's output redirected into a file run."
	if refusal != want {
		t.Fatalf("refusal = %q, want %q", refusal, want)
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(p, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Only files that exist are watched: a new file has no compiler errors to compare.
	snaps := WatchFiles(dir, FileWritesInCommand("sed -i 's/line2/LINE2/' x.txt; echo new > new.txt"))
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if err := os.WriteFile(p, []byte("line1\nLINE2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := filetracking.NewTracker()
	changes := CheckChangedFiles(snaps, tr)
	if len(changes) != 1 || changes[0].StartLine != 2 || changes[0].EndLine != 2 {
		t.Fatalf("unexpected changes: %+v", changes)
	}
	if got := FormatChangedFiles(changes); got != "\n\nx.txt changed at line 2." {
		t.Fatalf("report = %q", got)
	}
	if got := FormatChangedFiles([]WatchedFileChange{{Target: "a.go", StartLine: 3, EndLine: 5}}); got != "\n\na.go changed in lines 3-5." {
		t.Fatalf("report = %q", got)
	}
	if !tr.StateTracker().HasBeenRead(p) {
		t.Fatal("expected tracker to record modified file")
	}
}
