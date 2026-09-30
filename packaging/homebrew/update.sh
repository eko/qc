#!/bin/sh
# Points the Homebrew formula at a release: the source tarball of its tag
# and its sha256, in packaging/homebrew/qc.rb and in the tap's
# Formula/qc.rb, committed there (see RELEASING.md). Run it once the tag is
# final: moving the tag changes the tarball.
#
#   packaging/homebrew/update.sh <vX.Y.Z> <tap directory>
#
# CHECK=1 then builds the formula from the tap's commit through a temporary
# tap (brew install --build-from-source, brew test, brew audit); it replaces
# the installed qc for the time of the check. PUSH=1 pushes the tap.
set -eu

tag=$1
tap=$2
version=${tag#v}
formula=packaging/homebrew/qc.rb
url="https://github.com/eko/qc/archive/refs/tags/v${version}.tar.gz"

if [ ! -f "$tap/Formula/qc.rb" ] && [ ! -d "$tap/Formula" ]; then
	echo "$tap is not a Homebrew tap (no Formula directory)" >&2
	exit 1
fi

sha=$(curl -fsSL "$url" | shasum -a 256 | cut -d' ' -f1)
echo "v${version}: $sha"

sed -e "s|^  url \".*\"|  url \"${url}\"|" -e "s|^  sha256 \".*\"|  sha256 \"${sha}\"|" "$formula" >"$formula.new"
mv "$formula.new" "$formula"
cp "$formula" "$tap/Formula/qc.rb"

if git -C "$tap" diff --quiet -- Formula/qc.rb; then
	echo "the tap already points at v${version}"
else
	git -C "$tap" add Formula/qc.rb
	git -C "$tap" commit -q -m "qc ${version}"
	echo "committed \"qc ${version}\" in $tap"
fi

if [ "${CHECK:-}" = 1 ]; then
	check=eko/qccheck
	brew untap "$check" >/dev/null 2>&1 || true
	brew tap "$check" "$tap"
	trap 'brew uninstall "$check/qc" >/dev/null 2>&1 || true; brew untap "$check" >/dev/null 2>&1 || true' EXIT
	brew uninstall --ignore-dependencies qc >/dev/null 2>&1 || true
	HOMEBREW_NO_AUTO_UPDATE=1 brew install --build-from-source "$check/qc"
	HOMEBREW_NO_AUTO_UPDATE=1 brew test "$check/qc"
	HOMEBREW_NO_AUTO_UPDATE=1 brew audit --strict --online "$check/qc"
	echo "formula checked; reinstall with: brew install eko/tap/qc"
fi

if [ "${PUSH:-}" = 1 ]; then
	git -C "$tap" push
else
	echo "push it with: git -C $tap push"
fi
