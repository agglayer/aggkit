#!/usr/bin/env bash
# Tests for check_tag_ancestry.sh using a throwaway repository and a fake origin.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$script_dir/check_tag_ancestry.sh"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.com
export GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.com

git init -q --bare "$tmp/origin.git"
git init -q -b develop "$tmp/work"
cd "$tmp/work"
git remote add origin "$tmp/origin.git"

commit() { git commit -q --allow-empty -m "$1"; }

commit base
git tag v1.0.0
commit dev-only
git tag v1.1.0
git push -q origin develop

git checkout -q -b release/0.11 v1.0.0
commit rel-only
git tag v0.11.1
git tag v0.11.2-rc.1
git push -q origin release/0.11

git checkout -q --orphan attacker
git rm -rfq . 2>/dev/null || true
commit evil
git tag v9.9.9
git push -q origin attacker

git checkout -q develop

failures=0
run() { # name expected(0|1) ref
  local name="$1" want="$2" ref="$3" out got=0
  out="$tmp/gh_output"
  : >"$out"
  GITHUB_OUTPUT="$out" GITHUB_REF="$ref" "$script" >"$tmp/log" 2>&1 || got=$?
  if [[ "$got" -ne 0 ]]; then got=1; fi
  if [[ "$got" -eq "$want" ]]; then
    echo "PASS: $name"
  else
    echo "FAIL: $name (exit $got, wanted $want)"
    cat "$tmp/log"
    failures=$((failures + 1))
  fi
}

run "tag on develop" 0 refs/tags/v1.1.0
run "tag on develop and release" 0 refs/tags/v1.0.0
run "tag only on release/0.11" 0 refs/tags/v0.11.1
run "prerelease tag on release/0.11" 0 refs/tags/v0.11.2-rc.1
run "tag on unrelated branch" 1 refs/tags/v9.9.9
run "malformed tag" 1 refs/tags/v1.2
run "malformed tag prefix" 1 refs/tags/1.1.0
run "malformed prerelease" 1 'refs/tags/v1.1.0-bad_pre'
run "branch ref" 1 refs/heads/develop
run "empty ref" 1 ""

# Version output.
: >"$tmp/gh_output"
GITHUB_OUTPUT="$tmp/gh_output" GITHUB_REF=refs/tags/v0.11.2-rc.1 "$script" >/dev/null 2>&1
if grep -qx 'version=0.11.2-rc.1' "$tmp/gh_output"; then
  echo "PASS: version output"
else
  echo "FAIL: version output"
  failures=$((failures + 1))
fi

# Works without fetching when remote refs are already present.
git fetch -q --no-tags origin '+refs/heads/develop:refs/remotes/origin/develop' '+refs/heads/release/*:refs/remotes/origin/release/*'
got=0
SKIP_FETCH=1 GITHUB_REF=refs/tags/v0.11.1 "$script" >/dev/null 2>&1 || got=$?
if [[ "$got" -eq 0 ]]; then echo "PASS: SKIP_FETCH"; else echo "FAIL: SKIP_FETCH"; failures=$((failures + 1)); fi

if [[ "$failures" -ne 0 ]]; then
  echo "$failures test(s) failed"
  exit 1
fi
echo "all tests passed"
