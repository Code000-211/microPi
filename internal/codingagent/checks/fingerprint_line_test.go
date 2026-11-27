package checks

import "testing"

// A line number a checker writes into the message (TOML, YAML, go vet, bash) does not make a shifted pre-existing error new.
func TestFingerprintIgnoresLineNumbersInTheMessage(t *testing.T) {
	before := Diagnostic{File: "a.toml", Line: 2, Severity: SeverityError, Message: `toml: line 2 (last key "b"): expected value but found '\n' instead`}
	after := before
	after.Line, after.Message = 4, `toml: line 4 (last key "b"): expected value but found '\n' instead`
	if Fingerprint(before) != Fingerprint(after) {
		t.Fatalf("shifted error has a new fingerprint:\n%q\n%q", Fingerprint(before), Fingerprint(after))
	}
	other := before
	other.Message = `toml: line 2 (last key "c"): expected value but found '\n' instead`
	if Fingerprint(before) == Fingerprint(other) {
		t.Fatal("a different error has the same fingerprint")
	}
}
