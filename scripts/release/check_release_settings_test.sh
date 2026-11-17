#!/usr/bin/env bash
# Tests check_release_settings.sh against a stub `gh` that serves fixtures.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Stub gh: `gh api [flags] PATH` prints testdata/$SCENARIO/<path> (falling back
# to testdata/pass), with '/', '?', '&' and '=' mapped to '_'.
cat > "$TMP/gh" <<'STUB'
#!/usr/bin/env bash
[ "${1:-}" = api ] || { echo "stub gh: unsupported: $*" >&2; exit 2; }
shift
path=""
for a in "$@"; do case "$a" in -*) ;; *) path="$a"; break ;; esac; done
name="$(printf '%s' "$path" | tr '/?&=' '____')"
for dir in "$SCENARIO" pass; do
  if [ -f "$TESTDATA/$dir/$name" ]; then cat "$TESTDATA/$dir/$name"; exit 0; fi
done
echo "gh: Not Found (HTTP 404)" >&2
exit 1
STUB
chmod +x "$TMP/gh"

export TESTDATA="$HERE/testdata" REPO=acme/widget
FAILS=0

run() { # scenario -> sets OUT and RC
  OUT="$(PATH="$TMP:$PATH" SCENARIO="$1" "$HERE/check_release_settings.sh" "${@:2}" 2>&1)"
  RC=$?
}
expect() { # description, condition result
  if [ "$2" -eq 0 ]; then echo "ok   $1"; else echo "FAIL $1"; FAILS=$((FAILS + 1)); fi
}
has() { grep -Eq "$1" <<<"$OUT"; }

run pass
expect "all-pass: exit 0" "$([ "$RC" -eq 0 ]; echo $?)"
expect "all-pass: no FAIL lines" "$(has '^FAIL' && echo 1 || echo 0)"
expect "all-pass: tag ruleset PASS" "$(has '^PASS tag-ruleset' ; echo $?)"
expect "all-pass: environment PASS" "$(has '^PASS environment'; echo $?)"
expect "all-pass: teams PASS" "$(has '^PASS teams'; echo $?)"
expect "all-pass: both branch checks PASS" "$(has '^PASS branch-rules\[develop\]' && has '^PASS branch-rules\[release/1.1\]' && has '^PASS branch-rules\[release/1.2\]' && has '^PASS branch-rules\[release/1.10\]'; echo $?)"
expect "package ACL is MANUAL" "$(has '^MANUAL ghcr-package-acl'; echo $?)"
expect "package visibility reported" "$(has 'visibility: private'; echo $?)"

run no_ruleset
expect "missing tag ruleset: FAIL line" "$(has '^FAIL tag-ruleset'; echo $?)"
expect "missing tag ruleset: exit 1" "$([ "$RC" -eq 1 ]; echo $?)"

run no_self_review
expect "no prevent-self-review: FAIL line" "$(has '^FAIL environment.*prevent_self_review'; echo $?)"
expect "no prevent-self-review: exit 1" "$([ "$RC" -eq 1 ]; echo $?)"

run admin_bypass
expect "admin bypass on environment: FAIL line" "$(has '^FAIL environment.*can_admins_bypass'; echo $?)"
expect "admin bypass on environment: exit 1" "$([ "$RC" -eq 1 ]; echo $?)"

run branch_bypass
expect "branch ruleset bypass actor: FAIL line" "$(has '^FAIL branch-rules\[release/1.2\].*bypass actors'; echo $?)"
expect "branch ruleset bypass actor: exit 1" "$([ "$RC" -eq 1 ]; echo $?)"

run old_branch_weak
expect "older release branch without rules: FAIL line" "$(has '^FAIL branch-rules\[release/1.1\]'; echo $?)"
expect "older release branch without rules: exit 1" "$([ "$RC" -eq 1 ]; echo $?)"

run pass --json
expect "--json is a valid array" "$(jq -e 'type == "array" and length > 0' <<<"$OUT" >/dev/null; echo $?)"

[ "$FAILS" -eq 0 ] || { echo "$FAILS check(s) failed"; exit 1; }
echo "all tests passed"
