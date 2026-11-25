#!/usr/bin/env bash
# Tests apply_release_settings.sh against a stub `gh` that serves fixtures and
# records every call that is not a GET.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Stub gh: `gh api [-X METHOD] [flags] PATH` prints testdata/apply/$SCENARIO/<path>
# (falling back to testdata/apply/base), with '/', '?', '&' and '=' mapped to
# '_'. A file named <path>.404 makes the path answer 404; a missing file does too.
# Calls with a method other than GET are appended to $CALLS (method, path, body).
cat > "$TMP/gh" <<'STUB'
#!/usr/bin/env bash
[ "${1:-}" = api ] || { echo "stub gh: unsupported: $*" >&2; exit 2; }
shift
method=GET; path=""; input=""
while [ $# -gt 0 ]; do
  case "$1" in
    -X|--method) method="$2"; shift 2 ;;
    --input) input="$2"; shift 2 ;;
    -*) shift ;;
    *) path="$1"; shift ;;
  esac
done
if [ "$method" != GET ]; then
  body=""
  [ -z "$input" ] || body="$(cat | jq -cS .)"
  printf '%s %s %s\n' "$method" "$path" "$body" >> "$CALLS"
  echo '{}'
  exit 0
fi
name="$(printf '%s' "$path" | tr '/?&=' '____')"
for dir in "$SCENARIO" base; do
  if [ -f "$TESTDATA/$dir/$name.404" ]; then break; fi
  if [ -f "$TESTDATA/$dir/$name" ]; then cat "$TESTDATA/$dir/$name"; exit 0; fi
done
echo "gh: Not Found (HTTP 404)" >&2
exit 1
STUB
chmod +x "$TMP/gh"

export TESTDATA="$HERE/testdata/apply"
FAILS=0

run() { # scenario args... -> sets OUT, RC and CALLS
  local scenario="$1"; shift
  export CALLS="$TMP/calls"
  : > "$CALLS"
  OUT="$(PATH="$TMP:$PATH" SCENARIO="$scenario" "$HERE/apply_release_settings.sh" --repo acme/widget "$@" 2>&1)"
  RC=$?
}
expect() { # description, condition result
  if [ "$2" -eq 0 ]; then echo "ok   $1"; else echo "FAIL $1"; FAILS=$((FAILS + 1)); fi
}
has() { grep -Eq -- "$1" <<<"$OUT"; }
called() { grep -Eq -- "$1" "$CALLS"; }
ncalls() { wc -l < "$CALLS" | tr -d ' '; }

# A fresh repository: only the existing branch ruleset, no tag ruleset, no environment.
run base
expect "dry run: exit 0" "$([ "$RC" -eq 0 ]; echo $?)"
expect "dry run: no write call is made" "$([ "$(ncalls)" -eq 0 ]; echo $?)"
expect "dry run: prints the tag ruleset creation" "$(has 'would run: gh api -X POST repos/acme/widget/rulesets'; echo $?)"
expect "dry run: prints the branch rules update" "$(has 'would run: gh api -X PUT repos/acme/widget/rulesets/20'; echo $?)"
expect "dry run: prints the environment creation" "$(has 'would run: gh api -X PUT repos/acme/widget/environments/release'; echo $?)"
expect "dry run: prints the deployment policy" "$(has 'would run: gh api -X POST repos/acme/widget/environments/release/deployment-branch-policies'; echo $?)"
expect "dry run: shows a diff for the branch ruleset" "$(has '\+ *"refs/heads/release/\*"'; echo $?)"
expect "dry run: package requests are printed" "$(has 'ghcr.io/acme/widget'; echo $?)"
expect "dry run: states that nothing is changed" "$(has '4 call\(s\) would be made'; echo $?)"

run base --apply
expect "apply: exit 0" "$([ "$RC" -eq 0 ]; echo $?)"
expect "apply: four write calls" "$([ "$(ncalls)" -eq 4 ]; echo $?)"
expect "apply: tag ruleset limited to the release team" "$(called 'POST repos/acme/widget/rulesets .*"actor_id":1001.*"target":"tag"' ; echo $?)"
expect "apply: tag ruleset covers v* only for legacy" "$(called '"include":\["refs/tags/v\*"\]' && ! called 'refs/tags/\*/v'; echo $?)"
expect "apply: tag ruleset has the four rules" "$(called '"type":"creation"' && called '"type":"update"' && called '"type":"deletion"' && called '"type":"non_fast_forward"'; echo $?)"
expect "apply: branch rules go into the existing ruleset" "$(called 'PUT repos/acme/widget/rulesets/20 .*"refs/heads/release/\*"'; echo $?)"
expect "apply: no second branch ruleset" "$(! called 'POST repos/acme/widget/rulesets .*"target":"branch"'; echo $?)"
expect "apply: environment with reviewers, self review and admin bypass" "$(called 'PUT repos/acme/widget/environments/release .*"can_admins_bypass":false.*"prevent_self_review":true,"reviewers":\[\{"id":1002,"type":"Team"\}\]'; echo $?)"
expect "apply: tag deployment policy" "$(called 'POST repos/acme/widget/environments/release/deployment-branch-policies \{"name":"v\*","type":"tag"\}'; echo $?)"

# Already converged: nothing to do, whether or not it is a dry run.
run converged
expect "converged: exit 0" "$([ "$RC" -eq 0 ]; echo $?)"
expect "converged: reports no change" "$(has 'The live settings already match'; echo $?)"
run converged --apply
expect "converged apply: makes no call" "$([ "$(ncalls)" -eq 0 ]; echo $?)"
expect "converged apply: exit 0" "$([ "$RC" -eq 0 ]; echo $?)"

# Settings that exist but are wrong are brought back, without duplicates.
run drift --apply
expect "drift: exit 0" "$([ "$RC" -eq 0 ]; echo $?)"
expect "drift: tag ruleset updated in place" "$(called 'PUT repos/acme/widget/rulesets/21 '; echo $?)"
expect "drift: unknown bypass team removed, release team added, org admin kept" "$(called 'PUT repos/acme/widget/rulesets/21 .*"actor_id":1001.*"actor_type":"Team"' && ! called '"actor_id":77' && called '"actor_type":"OrganizationAdmin"'; echo $?)"
expect "drift: missing update and non_fast_forward rules added" "$(called 'PUT repos/acme/widget/rulesets/21 .*"type":"update"' && called 'PUT repos/acme/widget/rulesets/21 .*"type":"non_fast_forward"'; echo $?)"
expect "drift: branch ruleset gets one approval, deletion and no force push" "$(called 'PUT repos/acme/widget/rulesets/20 .*"required_approving_review_count":1' && called 'PUT repos/acme/widget/rulesets/20 .*"type":"deletion"' && called 'PUT repos/acme/widget/rulesets/20 .*"type":"non_fast_forward"'; echo $?)"
expect "drift: environment fixed" "$(called 'PUT repos/acme/widget/environments/release .*"can_admins_bypass":false.*"prevent_self_review":true'; echo $?)"
expect "drift: wrong branch policy removed" "$(called 'DELETE repos/acme/widget/environments/release/deployment-branch-policies/5'; echo $?)"
expect "drift: existing tag policy kept" "$(! called 'deployment-branch-policies/6' && ! called 'POST repos/acme/widget/environments/release/deployment-branch-policies'; echo $?)"

# Components: component environments and tag patterns are added to a converged legacy setup.
run converged --apply --components legacy,aggkit,sdk
expect "components: exit 0" "$([ "$RC" -eq 0 ]; echo $?)"
expect "components: tag ruleset also covers component tags" "$(called 'PUT repos/acme/widget/rulesets/21 .*"refs/tags/\*/v\*"'; echo $?)"
expect "components: branch rules also cover release/*/*" "$(called 'PUT repos/acme/widget/rulesets/20 .*"refs/heads/release/\*/\*"'; echo $?)"
expect "components: aggkit environment from aggkit/v* tags" "$(called 'PUT repos/acme/widget/environments/release-aggkit ' && called 'release-aggkit/deployment-branch-policies \{"name":"aggkit/v\*","type":"tag"\}'; echo $?)"
expect "components: sdk environment from sdk/v* tags" "$(called 'PUT repos/acme/widget/environments/release-sdk ' && called 'release-sdk/deployment-branch-policies \{"name":"sdk/v\*","type":"tag"\}'; echo $?)"
expect "components: legacy environment untouched" "$(! called 'environments/release '; echo $?)"
expect "components: npm request printed for sdk" "$(has 'npm owners of @agglayer/sdk'; echo $?)"
run converged --components dev_ui
expect "components: dev_ui package request" "$(has 'ghcr.io/acme/agglayer-dev-ui'; echo $?)"

# Missing teams: the dry run still shows everything with placeholders; apply does
# only what does not need them and fails.
run no_teams
expect "no teams dry run: exit 0" "$([ "$RC" -eq 0 ]; echo $?)"
expect "no teams dry run: placeholder id shown" "$(has '<id of team release-manager>'; echo $?)"
expect "no teams dry run: request for the org owners" "$(has 'To the organization owners'; echo $?)"
run no_teams --apply
expect "no teams apply: exit 1" "$([ "$RC" -eq 1 ]; echo $?)"
expect "no teams apply: only the branch rules are written" "$([ "$(ncalls)" -eq 1 ] && called 'PUT repos/acme/widget/rulesets/20'; echo $?)"

# Usage errors.
run converged --components bogus
expect "unknown component: exit 2" "$([ "$RC" -eq 2 ]; echo $?)"
run converged --nope
expect "unknown option: exit 2" "$([ "$RC" -eq 2 ]; echo $?)"

[ "$FAILS" -eq 0 ] || { echo "$FAILS check(s) failed"; exit 1; }
echo "all tests passed"
