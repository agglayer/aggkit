#!/usr/bin/env bash
# Verify that a release tag points at a commit reachable from origin/develop
# or from an origin/release/* branch, and emit the version (without "v").
#
# Usage: GITHUB_REF=refs/tags/v1.2.3 check_tag_ancestry.sh
#        check_tag_ancestry.sh refs/tags/v1.2.3
# Env:   REMOTE (default: origin), SKIP_FETCH=1 to skip fetching branches.
set -euo pipefail

ref="${1:-${GITHUB_REF:-}}"
remote="${REMOTE:-origin}"

if [[ -z "$ref" ]]; then
  echo "error: no ref given (set GITHUB_REF or pass it as first argument)" >&2
  exit 1
fi

if [[ ! "$ref" =~ ^refs/tags/v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "error: '$ref' is not a release tag (expected refs/tags/vX.Y.Z or refs/tags/vX.Y.Z-<pre>)" >&2
  exit 1
fi

tag="${ref#refs/tags/}"
version="${tag#v}"

if ! commit="$(git rev-parse --verify --quiet "refs/tags/${tag}^{commit}")"; then
  echo "error: cannot resolve tag '$tag' to a commit" >&2
  exit 1
fi

if [[ "${SKIP_FETCH:-}" != "1" ]]; then
  git fetch --no-tags "$remote" \
    '+refs/heads/develop:refs/remotes/origin/develop' \
    '+refs/heads/release/*:refs/remotes/origin/release/*'
fi

bases=()
if git rev-parse --verify --quiet refs/remotes/origin/develop >/dev/null; then
  bases+=("origin/develop")
fi
while IFS= read -r b; do
  bases+=("$b")
done < <(git for-each-ref --format='%(refname:short)' 'refs/remotes/origin/release/*')

for base in "${bases[@]}"; do
  if git merge-base --is-ancestor "$commit" "refs/remotes/$base"; then
    echo "ok: tag $tag ($commit) is an ancestor of $base"
    if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
      echo "version=$version" >>"$GITHUB_OUTPUT"
    fi
    exit 0
  fi
done

echo "error: tag $tag (commit $commit) is not an ancestor of origin/develop or any origin/release/* branch" >&2
exit 1
