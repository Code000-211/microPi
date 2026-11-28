<!-- Title: type(scope): what changed, e.g. fix(coding-agent): grep reads large files in pieces. Security fixes: report privately first (SECURITY.md). -->

Fixes #

**What and why**


**How it was tested**


**Checklist**

- [ ] `gofmt -l .` prints nothing, `go vet ./...` is clean, and the tests of the changed packages pass
- [ ] A test fails without this change (or: why not)
- [ ] Speed or memory may change: before/after numbers are above
- [ ] End-to-end behaviour changes: `cd tests/e2e && GOWORK=off go test .` passes
- [ ] No `CHANGELOG.md` edits
