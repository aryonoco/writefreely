#!/usr/bin/env bash
# Prints the tag the next build of the divbast branch takes: v<base>-divbast.<N>,
# where N is one more than the highest N among the repository's tags for that
# base, or 1 when it has none. Tags whose N is not a plain number are ignored.
#
# usage: next-tag.sh <base>, e.g. next-tag.sh 0.17.3
set -euo pipefail

if [[ $# -ne 1 || ! $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo "usage: ${0##*/} <base>, an upstream release such as 0.17.3" >&2
	exit 2
fi
base=$1
prefix="v${base}-divbast."

# git tag's glob takes base's dots literally; the regex escapes them to match.
# grep exits 1 when nothing matches, which here means there is no tag yet
n=$(git tag --list "${prefix}*" |
	{ grep -E "^v${base//./\\.}-divbast\.[0-9]+\$" || true; } |
	sed -E 's/.*\.//' |
	sort -n |
	tail -n 1)

echo "${prefix}$((10#${n:-0} + 1))"
