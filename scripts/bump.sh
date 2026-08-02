#!/usr/bin/env bash
#
# Release version bump: syncs VERSION, CHANGELOG.md and the README version badge.
#
# It deliberately stops there. In Go the git tag *is* the published version — the
# moment `vX.Y.Z` is pushed, the proxy can serve it and it can never be changed.
# Committing and tagging stay a separate, deliberate step (`make tag`).
#
#   ./scripts/bump.sh 0.2.0

set -euo pipefail

version="${1:-}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

die() {
	echo "bump: $*" >&2
	exit 1
}

[ -n "$version" ] || die "usage: ./scripts/bump.sh X.Y.Z"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "'$version' is not X.Y.Z"

cd "$root"

for f in VERSION CHANGELOG.md README.md; do
	[ -f "$f" ] || die "missing $f"
done

current="$(tr -d '[:space:]' <VERSION)"
[ "$current" != "$version" ] || die "already at v$version"

# The changelog entry is the point of the release, so refuse to cut one without
# it rather than silently producing an empty section.
body="$(awk '
	/^## \[?Unreleased\]?[[:space:]]*$/ { found = 1; next }
	found && /^## / { exit }
	found { print }
' CHANGELOG.md)"

grep -q '^[[:space:]]*[-*]' <<<"$body" || die "CHANGELOG.md: [Unreleased] has no entries yet"

date="$(date -u +%Y-%m-%d)"

# Leave [Unreleased] in place and open a dated section right below it: everything
# that was unreleased becomes the body of the new version.
awk -v ver="$version" -v date="$date" '
	state == "" {
		print
		if ($0 ~ /^## \[?Unreleased\]?[[:space:]]*$/) {
			state = "moved"
			print ""
			print "## [" ver "] - " date
		}
		next
	}
	{ print }
' CHANGELOG.md >CHANGELOG.md.tmp && mv CHANGELOG.md.tmp CHANGELOG.md

printf '%s\n' "$version" >VERSION

# sed -i is spelled differently on BSD and GNU, so write through a temp file.
sed -E "s#(img\.shields\.io/badge/version-)[^-\"']+(-blue)#\1${version}\2#" README.md >README.md.tmp
if cmp -s README.md README.md.tmp; then
	rm -f README.md.tmp
	die "README.md: version badge not found"
fi
mv README.md.tmp README.md

echo "Bumped $current → $version"
echo "  VERSION"
echo "  CHANGELOG.md"
echo "  README.md (badge)"
echo
echo "Next: review the diff, then"
echo "  make tag"
