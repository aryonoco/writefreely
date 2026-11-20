#!/usr/bin/env bash
# Tests next-tag.sh against scratch repositories holding known tags. Exits
# non-zero at the first case whose answer is wrong, naming it.
#
# usage: bash .github/divbast/next-tag_test.sh
set -euo pipefail

script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/next-tag.sh"
scratch=$(mktemp -d)
trap 'rm -rf "${scratch:?}"' EXIT

# Neither the caller's git configuration nor the system's, so a signing or
# hook setting there can't change what the scratch repositories hold
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

# check <case> <want> [<tag>...]: in a fresh repository whose one commit
# carries the tags, next-tag.sh 0.17.3 must print <want>
check() {
	local name=$1 want=$2 repo got t
	shift 2
	repo="${scratch}/${name//[^a-z0-9]/-}"
	git init -q -b main "${repo}"
	git -C "${repo}" -c user.name=test -c user.email=test@example.invalid \
		commit -q --allow-empty -m init
	for t in "$@"; do
		git -C "${repo}" tag "${t}"
	done
	got=$(cd "${repo}" && "${script}" 0.17.3) || got="exit status $?"
	if [[ ${got} != "${want}" ]]; then
		echo "FAIL: ${name}: got '${got}', want '${want}'" >&2
		exit 1
	fi
	echo "ok: ${name}"
}

check "no tags" v0.17.3-divbast.1

check "the highest of 1 and 9, beside an upstream tag" v0.17.3-divbast.10 \
	v0.17.3 v0.17.3-divbast.1 v0.17.3-divbast.9

# 10 sorts before 9 as text, so only a numeric sort finds 10 the highest
check "past 9 and 10, a numeric not a lexical sort" v0.17.3-divbast.11 \
	v0.17.3-divbast.2 v0.17.3-divbast.9 v0.17.3-divbast.10

check "malformed tags ignored" v0.17.3-divbast.3 \
	v0.17.3-divbast.2 v0.17.3-divbast.x v0.17.3-divbast.5x \
	v0.17.3-divbast.7.1

check "other bases ignored" v0.17.3-divbast.2 \
	v0.17.3-divbast.1 v0.17.4-divbast.6 v0.17.30-divbast.8
