package tools

import "testing"

// µPi: the check reads quotes as bash does, so a ; or a command hidden behind an escaped quote is seen; options that write or run a program
// are refused. Each of these ran its second command under bash while the check took it as read-only.
func TestIsReadOnlyCommandReadsQuotesAsBashDoes(t *testing.T) {
	for _, command := range []string{
		`echo "a\" "; touch pwned # "`,
		`echo $'\' '; touch pwned #'`,
		`cat a # comment`,
		`sort --compress-program=sh big.txt`,
		`date -s 2020-01-01`,
		`date --set=2020-01-01`,
		`file -C -m magic`,
	} {
		if IsReadOnlyCommand(command) {
			t.Errorf("IsReadOnlyCommand(%q) = true", command)
		}
	}
	for _, command := range []string{`echo "a \"quoted\" word"`, `grep -n "x\\$" f.txt`, `echo $'tab\tend'`, `date +%s`, `date -u`} {
		if !IsReadOnlyCommand(command) {
			t.Errorf("IsReadOnlyCommand(%q) = false, want true", command)
		}
	}
}
