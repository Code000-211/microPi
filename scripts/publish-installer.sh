#!/usr/bin/env bash
# Publishes install.sh, install.ps1 (Windows: `irm https://upi.opensec.in/install.ps1 | iex`) and site/ (the home page, the
# crash-report page, the logo, the page's font, its video and poster, and the documentation in site/docs, built by
# scripts/build-docs.py) to the gh-pages
# branch, which GitHub Pages serves at https://upi.opensec.in.
# Usage: scripts/publish-installer.sh [REMOTE]     (default remote: origin). Adds a commit to gh-pages; never rewrites it.
source "$(dirname "$0")/lib/common.sh"
need git
need python3
python3 "$PIBOLT_ROOT/scripts/build-docs.py" --check
REMOTE="${1:-origin}"
WORKTREE="$(mktemp -d)"
trap 'git -C "$PIBOLT_ROOT" worktree remove --force "$WORKTREE" 2>/dev/null || true' EXIT
git -C "$PIBOLT_ROOT" fetch -q "$REMOTE" gh-pages
git -C "$PIBOLT_ROOT" worktree add -q --detach "$WORKTREE" FETCH_HEAD
cp "$PIBOLT_ROOT/install.sh" "$WORKTREE/install.sh"
cp "$PIBOLT_ROOT/install.ps1" "$WORKTREE/install.ps1"
find "$PIBOLT_ROOT/site" -maxdepth 1 -type f -exec cp {} "$WORKTREE/" \;
# (The documentation is replaced as a whole, so that a page removed from the sources is removed from the site too.)
rm -rf "$WORKTREE/docs"
cp -R "$PIBOLT_ROOT/site/docs" "$WORKTREE/docs"
git -C "$WORKTREE" add install.sh install.ps1 "$WORKTREE"/*.html "$WORKTREE"/*.svg "$WORKTREE"/*.woff2 "$WORKTREE"/*.txt "$WORKTREE"/*.mp4 "$WORKTREE"/*.jpg docs
# (Staged first, so that a file gh-pages does not have yet counts as a change.)
if git -C "$WORKTREE" diff --cached --quiet; then
	log "gh-pages already has these installers and site"
	exit 0
fi
git -C "$WORKTREE" commit -q -m "chore(pages): update the installers and site"
git -C "$WORKTREE" push -q "$REMOTE" HEAD:gh-pages
log "published install.sh and install.ps1 to $REMOTE gh-pages"
