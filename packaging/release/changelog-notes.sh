#!/bin/sh
# Prints the release notes of a version: its "## [X.Y.Z]" section of
# CHANGELOG.md, without the heading and the link definitions. Fails when the
# section is missing or empty.
#
#   packaging/release/changelog-notes.sh 0.1.0 [CHANGELOG.md]
set -eu

version=${1:?usage: changelog-notes.sh VERSION [CHANGELOG]}
changelog=${2:-CHANGELOG.md}

notes=$(awk -v v="$version" '
	index($0, "## [" v "]") == 1 { found = 1; next }
	found && /^## \[/ { exit }
	found && /^\[[^]]*\]: / { next }
	found { print }
' "$changelog")

if [ -z "$(printf '%s' "$notes" | tr -d '[:space:]')" ]; then
	echo "no '## [$version]' section in $changelog: add the release notes before tagging" >&2
	exit 1
fi

printf '%s\n' "$notes"
