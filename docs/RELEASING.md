# Releasing µPi

Five workflows, all on GitHub's own runners (Ubuntu 24.04 x86-64 and ARM64, Windows Server 2025, macOS 15 on Apple silicon):

| Workflow | When | What |
|---|---|---|
| `ci` | every push to `pi-bolt`, every pull request | lint and types (the workflows too, with actionlint), Pi's tests, µPi built and tested on Linux x86-64, Linux ARM64, macOS (Apple silicon: its tests and its comparison with stock Pi side by side) and Windows x64, the installer's checks, whether the engine patches apply. A pull request that changes only documentation builds and tests nothing |
| `release` | a push of an `upi-vX.Y.Z` tag, or by hand | checks the runtimes' attestations, builds the 20 archives on Linux (x86-64 and ARM64), macOS and Windows, tests each as unpacked from its archive, attests each, makes a **draft** release with `SHA256SUMS` and `RUNTIME_STAMP` ([below](#when-the-engine-changed)). Nothing is signed or published here |
| `publish` | by hand, on the draft | signs `SHA256SUMS` with the release key, checks the signature and the checksums, and publishes the GitHub release ([Signing](#signing)). GitHub only: no npm, no Homebrew, no site |
| `runtime` | by hand, when the engine changed | builds the four runtimes, each on its platform's runner, attests them, and attaches them to the tag's draft ([below](#when-the-engine-changed)) |
| `upstream` | daily | opens an issue when Pi has a new release, with the files where merging it conflicts |

Tags are pushed by hand (`git tag -a upi-vX.Y.Z -m "µPi X.Y.Z" && git push origin upi-vX.Y.Z`). The release key is a secret
of the `release` environment, which only `publish` runs from `pi-bolt` and waits for a reviewer ([Signing](#signing)).

- [A release, step by step](#a-release-step-by-step)
- [What a release holds](#what-a-release-holds)
- [When the engine changed](#when-the-engine-changed)
- [Provenance](#provenance)
- [A new Pi version](#a-new-pi-version)
- [The macOS builds](#the-macos-builds)
- [The Windows builds](#the-windows-builds)
- [Signing](#signing)

## A release, step by step

1. Write the release notes in `docs/releases/X.Y.Z.md`; they become the GitHub release's text.
2. `scripts/bump-version.sh` (the last number goes up by one, 0.7.0 to 0.7.1; `--version X.Y.Z` sets another), commit, and push
   to `pi-bolt` (or merge a pull request).
3. When the engine changed, build the runtimes first ([When the engine changed](#when-the-engine-changed)); `release` takes the
   previous release's otherwise, and with neither there is no release to build.
4. Push the tag `upi-vX.Y.Z`. `release` builds and tests for about an hour and leaves a draft:
   `gh release download upi-vX.Y.Z -R Code000-211/microPi -p SHA256SUMS -p RUNTIME_STAMP -D dist/X.Y.Z` (a dry run of
   `release` from the Actions tab, with `dry_run`, builds and tests and makes no draft at all).
5. Once the draft's checksums look right, run `publish` from `pi-bolt`: `gh workflow run publish.yml -R Code000-211/microPi
   -f tag=upi-vX.Y.Z`. It signs `SHA256SUMS`, checks the files and the signature, and publishes.

A draft can also be signed on a machine that has the key, before `publish` (then `publish` verifies the signature instead of
making one):

```bash
gh release download upi-vX.Y.Z -R Code000-211/microPi -p SHA256SUMS -D dist/X.Y.Z --clobber
scripts/sign-release.sh --key micropi-signing-key.pem dist/X.Y.Z
gh release upload upi-vX.Y.Z -R Code000-211/microPi dist/X.Y.Z/SHA256SUMS.sig dist/X.Y.Z/SHA256SUMS.sshsig
```

`publish` refuses a draft whose signature does not verify against `keys/release.pub`. Either workflow can be run again with the
same tag: the draft's files are replaced (a signature of the old `SHA256SUMS` is dropped), and a published release is left as it is.

### A pre-release first

A release can be staged before it is made the latest: `publish` with `-f prerelease=true` publishes it as a GitHub pre-release.
Only the beta channel takes it (`upi update --channel beta`, or `MICROPI_CHANNEL=beta` for the installers). Running `publish`
again for the same tag without it makes the release the latest.

```bash
gh workflow run publish.yml -R Code000-211/microPi -f tag=upi-vX.Y.Z -f prerelease=true
gh workflow run publish.yml -R Code000-211/microPi -f tag=upi-vX.Y.Z      # later: the latest release
```

A pre-release is a version like any other: one that needs a fix is followed by the next version, not replaced.

Old-distribution and old-CPU checks (`tests/compat/run.sh`: CentOS 7, Debian 9, Amazon Linux 2, emulated Haswell to Nehalem)
need bubblewrap and qemu-user, which GitHub's runners do not allow; run them on a Linux machine before a release that changes
the engine or the build. On Windows, the same for older CPUs is Intel SDE (`-snb`: no AVX2, the bytecode is used; `-hsw`).

## What a release holds

- The archives: `upi-linux-x64`, `-baseline` and `-jit`, `upi-linux-arm64` and `-jit`, and `upi-darwin-arm64` and
  `-jit` (`.tar.xz` and `.tar.gz`); `upi-win32-x64` and `-jit` (`.zip`); the runtimes `upi-runtime-linux-x64.tar.gz`,
  `upi-runtime-linux-arm64.tar.gz`, `upi-runtime-darwin-arm64.tar.gz` and `upi-runtime-win32-x64.zip`.
- The compile kits `upi-kit-linux-x64.tar.gz`, `upi-kit-linux-arm64.tar.gz` and `upi-kit-darwin-arm64.tar.gz`: the
  files of Pi each platform's default build read, its compile command and the training profile (`scripts/make-kit.mjs`, made
  by `scripts/build-pi.sh --kit` in `package-release.sh`). `upi compile` builds with them and the runtimes.
- `RUNTIME_STAMP`: what engine the runtimes were built from (below).
- `install.sh` and `install.ps1`, as they are in the tagged commit. `upi update` runs the release's own installer, and only
  once `SHA256SUMS` verifies with the release key built into the executable and lists the installer's checksum.
- `SHA256SUMS`: its first line is `# upi X.Y.Z`, then one line for each archive, `RUNTIME_STAMP`,
  `install.sh` and `install.ps1`.
  `install.ps1` refuses checksums whose first line is not its version: an older release, signed all the same, served as a newer one.
- `SHA256SUMS.sig` ([Signing](#signing)).

`scripts/release-files.txt` names the files of a release. `release` and `publish` check that `SHA256SUMS` lists exactly those, each as it is
(`scripts/check-release-files.sh`). A new platform or variant is added to that list.

## When the engine changed

`release` uses the previous release's runtimes when the engine is the same: `RUNTIME_STAMP` is the hash of the engine entries of
`sources.json`, the patches, the runtime build scripts and the linker-order hints they link with (`profiles/runtime-*.hints`)
(`scripts/runtime-stamp.sh`). `ci` says when it differs. The previous release's stamp counts only as its signed `SHA256SUMS`
gives it, for that release's version (releases before 0.8.0 did not sign it, so their runtimes are not reused). Runtimes
uploaded to the draft are hashed by `release`'s first job and checked again by each build job before it uses one.

The first release has no previous one and no draft to take runtimes from: `release` fails with an error that says to run
`runtime` first. When the engine changed (or for the first release), build the four runtimes before `release` runs, by the
`runtime` workflow, from `pi-bolt` once the change (or the release's commit) is there:

```bash
gh workflow run runtime.yml -R Code000-211/microPi -f tag=upi-vX.Y.Z [-f ref=<the commit the release is tagged on>]
```

It builds each runtime on its platform's runner (Linux x86-64 and ARM64, macOS, Windows), runs `tests/aot` and `tests/runtime`
on it, attests the four archives ([Provenance](#provenance)) and attaches `upi-runtime-<platform>.tar.gz` (Windows: `.zip`) to
the release `tag=upi-vX.Y.Z` names (making the draft if there is none). The runners are smaller than
the machine [BUILDING.md](BUILDING.md) asks for, so the build runs with swap and fewer jobs, for two to four hours. A branch
`runtime/<name>` builds its commit's runtimes the same way, as workflow artifacts, to try an engine change. Then push the tag (or
run `release` for it): it builds Pi on those runtimes.

By hand, on each platform: `scripts/toolchain/make-sysroot.sh` once (Linux), `scripts/fetch-sources.sh` and
`scripts/build-runtime.sh` ([BUILDING.md](BUILDING.md); Linux ARM64 can be the Mac's Docker, [Linux ARM64 in a
container](BUILDING.md#linux-arm64-in-a-container)), on Windows `scripts\fetch-sources.ps1` and `scripts\build-runtime.ps1`
([WINDOWS.md](WINDOWS.md)); then `scripts/package-release.sh --runtime-only` (Windows: `scripts\package-release.ps1
-RuntimeOnly`), and upload `dist/X.Y.Z/upi-runtime-<platform>.tar.gz` (Windows: `.zip`) to a draft of the tag:
`gh release create upi-vX.Y.Z --draft --title "µPi X.Y.Z" --notes ""`, then `gh release upload upi-vX.Y.Z <archive>`.
A runtime built by hand has no attestation: `release` refuses it ([Provenance](#provenance)); a draft's runtimes are also
hashed by first job and build jobs, so a changed one stops the release by itself.
`fetch-sources` stops when `.work/webkit` or `.work/bun` is not the pinned commit with the current patch applied (sources of an
earlier engine, or a fetch that stopped halfway): delete that folder and run it again.

Say in the release notes, for each runtime, where it was built, the engine commits it was built from (`.work/webkit` and
`.work/bun`, whose trees the patches make) and its SHA-256, so that it can be built again elsewhere and compared. (A runtime is
not yet built bit for bit the same twice; the prebuilt heap of a Pi build is checked that way: `-VerifyDeterminism`.)

## Provenance

Every archive of a release has a build provenance attestation (`actions/attest-build-provenance`): a statement of which
workflow run built it, from which commit, signed with that run's GitHub identity through Sigstore and kept in the repository's
[attestations](https://github.com/Code000-211/microPi/attestations). `runtime` attests the runtimes it builds; `release`'s build
jobs attest every archive they make, dry runs included. It is a second check beside the signature of `SHA256SUMS`: the signature
says the release key's holder approved a file, and the attestation says which run made it.

To check a downloaded file (GitHub CLI 2.49 or later):

```bash
gh attestation verify upi-linux-x64.tar.xz -R Code000-211/microPi
# Stricter: made by `release` run from `pi-bolt` (the tag's default case) or from the release's tag.
gh attestation verify upi-linux-x64.tar.xz -R Code000-211/microPi \
  --signer-workflow Code000-211/microPi/.github/workflows/release.yml --source-ref refs/heads/pi-bolt
```

A runtime that `runtime` built and that is still on a draft is checked with
`--signer-workflow Code000-211/microPi/.github/workflows/runtime.yml --source-ref refs/heads/pi-bolt`. An attestation names the
commit its run started from: `release` builds the tag's commit, and `runtime` run with `-f ref=...` builds that commit, which
the run's inputs give.

`release`'s first job checks each runtime the build will use before anything is built, and stops the release when one does not
verify:

- runtimes uploaded to the draft: attested by `runtime.yml` run from `pi-bolt`. A run from another branch (a `runtime/<name>`
  trial) does not count.
- the previous release's runtimes, used again: first checked against that release's signed `SHA256SUMS`. Each release packages
  the runtime again (a new `.tar.gz` or `.zip`, so a new SHA-256), so these are attested by that release's run of `release.yml`,
  from `pi-bolt` or from its tag (`runtime.yml` from `pi-bolt` counts too).

There are no exceptions: a runtime without an attestation by those workflows stops the release.

## A new Pi version

`upstream` opens an issue for each new Pi release. A scheduled agent follows it every day: it merges the release on a branch,
records the profile, opens a pull request and merges it when `ci` passes, after which the tag, `release` and `publish` take it
out. By hand:

1. Merge the tag into `pi-bolt` (`git fetch https://github.com/earendil-works/pi.git tag vX.Y.Z && git merge vX.Y.Z`). Where
   µPi changed the same code, keep both: µPi's changes are its commits on top of the previous Pi tag
   (`git log vX.Y.Z..pi-bolt -- packages/`).
2. `scripts/bump-version.sh --pi X.Y.Z` (the last number goes up by one, as for any release), `scripts/prepare-pi.sh --refresh-models`
   (the model catalog of the new Pi, `packages/ai/src/providers/data`), then `scripts/train-profile.sh` **on Linux**, and commit
   `profiles/pi-X.Y.Z` and the catalog. A profile recorded on Linux serves every platform (recorded on macOS first with
   `--out DIR` and merged in on Linux with `--with DIR`, it also places what only macOS runs). On Windows, `scripts\train-heap.ps1`
   then adds the prebuilt heap's order to it (`heap-functions.txt`, and the strings' order in `bytecode.order`): commit that too.
   The `heap-profile` workflow runs it on the Windows runner with the previous release's runtime
   (`gh workflow run heap-profile.yml -R Code000-211/microPi --ref <branch>`), uploads the profile as an artifact and pushes it to
   the branch `heap-profile/pi-X.Y.Z`; open a pull request from that branch by hand.
3. Push; `ci` builds and tests it, and it is released as above.

## The macOS builds

`release` builds them on the macOS 15 runners (Apple silicon), for macOS 13 and later: `scripts/check-macos-floor.sh` checks that
no executable or library asks for a later macOS. The executables are signed ad hoc, as `bun build --compile`
signs them, which is what an install through `install.sh` needs: neither quarantines what it downloads. A build downloaded
with a browser is quarantined, and Gatekeeper only accepts a Developer ID signature that is notarized. Notarization requires the
hardened runtime, under which the executable needs the entitlement `com.apple.security.cs.disable-library-validation` to map
its compiled code from its own file (and the `-jit` build `com.apple.security.cs.allow-jit`);
[ARCHITECTURE.md](https://github.com/earendil-works/pi/blob/main/docs/ARCHITECTURE.md#the-macos-arm64-port) says why.

## The Windows builds

`release` builds them on the Windows runners, on the Windows runtime: `scripts\package-release.ps1` makes
`upi-win32-x64.zip` (JIT off) and `upi-win32-x64-jit.zip` (JIT on, for extensions that do heavy JavaScript work at run
time: [PLUGINS.md](PLUGINS.md)). Each is built twice to check that its prebuilt heap does not depend on where the runtime was
loaded (`-VerifyDeterminism`); that check sometimes fails on one record of the heap ([WINDOWS.md](WINDOWS.md#the-design)), and `release` builds
again, up to three times, so that only a build whose two copies agreed is released. Each archive is then tested as unpacked:
`tests\pi\run.ps1`, `scripts\windows-selftest.ps1`, and `tests\runtime\run.ps1`.

`install.ps1` verifies the signature itself (it carries an Ed25519 verifier, since Windows has none), and refuses a Windows
release without one. The executables carry no Authenticode signature yet: SmartScreen may warn about one downloaded with a
browser (the installers do not mark what they download). An Authenticode signature, with the owner's certificate, would go on
`upi.exe` before the archive is made, as it changes the file and so its checksum. Never ask users to turn SmartScreen off or
to exclude µPi from Defender.

## Signing

`SHA256SUMS` in every release is signed with an Ed25519 key, twice: `SHA256SUMS.sig` (raw Ed25519) and `SHA256SUMS.sshsig`
(the same key in OpenSSH's signature format, namespace `upi-release`, made by `scripts/lib/sshsig.mjs`). The installer carries
the public key and refuses a download whose signature is missing or does not verify. It checks it with OpenSSL 3 (on `PATH` or
Homebrew's), or else with `ssh-keygen -Y verify` against `SHA256SUMS.sshsig` (OpenSSH 8.1 or later, which every current Mac
has), or else with the µPi already installed (which must print the SHA-256 of the checksums it verified; an exit status alone
does not count). Where none of them can check it, nothing is installed unless `MICROPI_UNVERIFIED` is the SHA-256 of that
release's `SHA256SUMS` (the installer shows it): the value matches one download only, so one left in a profile lets nothing else
in. `install.ps1` takes `MICROPI_ALLOW_UNSIGNED` (a build of one's own) the same way.
`install.ps1`, `scripts/fetch-runtime.sh` and the `release` workflow require the release's signature too: the previous
release's runtimes are reused only after their `SHA256SUMS` verifies.

`publish` signs with the secret `MICROPI_SIGNING_KEY` of the `release` environment. The environment accepts only workflows run
from `pi-bolt` and waits for a reviewer, a secret cannot be read back from GitHub, and workflows of pull requests from forks
get no secrets. `publish` signs only a tag whose commit is on `pi-bolt`, and what runs in that job (the checks,
`keys/release.pub`) is `pi-bolt`'s as the run was started, not the tag's. It signs only a `SHA256SUMS` that lists exactly the
files of a release, each checked, and only with the key of `keys/release.pub`. The check of the files runs in a step of its
own, without the key.

Two repository settings complete this, and are the owner's to set: a tag ruleset (Settings → Rules → Rulesets → New tag
ruleset) for `refs/tags/upi-v*` that lets only administrators create, move or delete such tags, and required reviewers on the
`release` environment (Settings → Environments → release), so that a signing run waits for one.

To set the key, as an administrator, on the machine that has it:

```bash
gh secret set MICROPI_SIGNING_KEY -R Code000-211/microPi --env release < micropi-signing-key.pem
```

To make the key pair:

```bash
openssl genpkey -algorithm ed25519 -out micropi-signing-key.pem
openssl pkey -in micropi-signing-key.pem -pubout -out keys/release.pub
```

Then copy the contents of `keys/release.pub` into `RELEASE_KEY` in `install.sh`, the output of
`node scripts/lib/sshsig.mjs pubkey keys/release.pub` into `RELEASE_SSH_KEY` in `install.sh`, and its base64 line into `$PiBoltReleaseKey` in `install.ps1`, commit them, set the secret as above, and keep a copy of `micropi-signing-key.pem` somewhere safe and private (a
password manager), never in the repository. Rotating the key is the same procedure; releases signed with the old key stay
verifiable with an installer that carries the old key.
