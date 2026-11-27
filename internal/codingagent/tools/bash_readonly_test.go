package tools

import "testing"

// Ports microPi's bash-read-only.test.ts.
func TestIsReadOnlyCommandTakesCommandsThatOnlyRead(t *testing.T) {
	for _, command := range []string{
		"git status --short",
		"git diff HEAD~1 -- src/a.ts",
		"git -C packages/ai log --oneline -5",
		"git --no-pager show HEAD:README.md",
		`rg -n "fn main" src`,
		"grep -rn 'needle' . | head -20",
		"cat a.txt b.txt | wc -l",
		"cd core && ls -la",
		"find . -name '*.rs' -type f",
		"ls src; ls test",
		"tail -n 50 build.log",
		"sort -u names.txt",
		"git log --format='%h %s' -3 || true",
	} {
		if !IsReadOnlyCommand(command) {
			t.Errorf("IsReadOnlyCommand(%q) = false, want true", command)
		}
	}
}

func TestIsReadOnlyCommandRefusesWritesOtherProgramsAndUnlisted(t *testing.T) {
	for _, command := range []string{
		"git commit -m x",
		"git branch -D old",
		"git diff --output=patch.diff",
		"git grep -O needle",
		"git -c core.pager=less log",
		"cat a.txt > b.txt",
		"echo hi >> log",
		"cat < a.txt",
		"ls 2>&1",
		"sleep 10 &",
		"echo $(rm -rf x)",
		"echo `touch x`",
		"find . -name '*.tmp' -delete",
		"find . -exec rm {} ;",
		"rg --pre ./script needle",
		"tail -f log",
		"sort -o out.txt in.txt",
		"FOO=1 ls",
		"ls && rm x",
		"ls\nrm x",
		"npm test",
		"cargo build",
		"sed -n 1p a.txt",
		"xargs rm < list",
		"ls && cd x",
		"cat 'unterminated",
		"",
		"git -C",
	} {
		if IsReadOnlyCommand(command) {
			t.Errorf("IsReadOnlyCommand(%q) = true, want false", command)
		}
	}
}
