#!/usr/bin/env bash
# Audits the repository and organization settings that gate releases.
# Read-only: only GET calls are made through the GitHub CLI.
# Branch rules are read from rulesets only; classic branch protection is not checked.
#
# Usage: check_release_settings.sh [--json]
# Env:   REPO (agglayer/aggkit), ORG (owner of REPO), APPROVERS_TEAM
#        (release-approvers), RELEASE_TEAM (release-manager), ENVIRONMENT (release)
# Output: one line per control: PASS|FAIL|MANUAL <control>: <detail>
# Exit:   1 when any control FAILs, otherwise 0.
set -uo pipefail

REPO="${REPO:-agglayer/aggkit}"
ORG="${ORG:-${REPO%%/*}}"
APPROVERS_TEAM="${APPROVERS_TEAM:-release-approvers}"
RELEASE_TEAM="${RELEASE_TEAM:-release-manager}"
ENVIRONMENT="${ENVIRONMENT:-release}"
TAG_PATTERN='v*'

JSON=0
case "${1:-}" in
  --json) JSON=1 ;;
  "") ;;
  *) echo "usage: $0 [--json]" >&2; exit 2 ;;
esac

for tool in gh jq; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 2; }
done

RESULTS=()
FAILED=0

report() { # status control detail
  RESULTS+=("$(jq -cn --arg s "$1" --arg c "$2" --arg d "$3" '{status:$s,control:$c,detail:$d}')")
  [ "$1" = FAIL ] && FAILED=1
  [ "$JSON" -eq 1 ] || printf '%s %s: %s\n' "$1" "$2" "$3"
}

# api PATH: on success stores the body in API_BODY. On failure sets API_STATUS
# to the HTTP status (or "error") and returns 1. Never call it in a subshell.
API_STATUS=""
API_BODY=""
# api PATH [paginate]: with a second argument the endpoint is a JSON array that
# is fetched page by page and merged into one array.
api() {
  local err raw
  err="$(mktemp)"
  API_BODY=""
  if raw="$(gh api ${2:+--paginate} "$1" 2>"$err")"; then
    if [ -n "${2:-}" ]; then
      raw="$(jq -s 'add // []' <<<"$raw")" || { rm -f "$err"; API_STATUS=error; return 1; }
    fi
    API_BODY="$raw"
    rm -f "$err"; API_STATUS=200; return 0
  fi
  API_STATUS="$(grep -o 'HTTP [0-9]*' "$err" | head -n1 | cut -d' ' -f2)"
  API_STATUS="${API_STATUS:-error}"
  rm -f "$err"
  return 1
}

# --- 4 (done first: the tag ruleset check needs the release team id) -------
TEAM_ID=""
check_teams() {
  local slug body missing=""
  for slug in "$RELEASE_TEAM" "$APPROVERS_TEAM"; do
    if api "orgs/$ORG/teams/$slug"; then body="$API_BODY"
      [ "$slug" = "$RELEASE_TEAM" ] && TEAM_ID="$(jq -r '.id' <<<"$body")"
    elif [ "$API_STATUS" = 404 ] || [ "$API_STATUS" = 403 ]; then
      missing="$missing $slug($API_STATUS)"
    else
      missing="$missing $slug(error)"
    fi
  done
  if [ -z "$missing" ]; then
    report PASS teams "$RELEASE_TEAM and $APPROVERS_TEAM exist in $ORG"
  else
    report MANUAL teams "could not read:$missing; the API answers 404 for a missing team and also for a team the token cannot see, so grant the token org read (read:org) or check https://github.com/orgs/$ORG/teams"
  fi
}

# --- 1 ---------------------------------------------------------------------
check_tag_ruleset() {
  local list ids id summary src detail found=0 best="" ok
  if ! api "repos/$REPO/rulesets?targets=tag&includes_parents=true" paginate; then
    report MANUAL tag-ruleset "cannot read rulesets (HTTP $API_STATUS); check https://github.com/$REPO/settings/rules"
    return
  fi
  list="$API_BODY"
  ids="$(jq -r '.[] | "\(.id)\t\(.source_type // "Repository")\t\(.source // "")"' <<<"$list")"
  while IFS=$'\t' read -r id summary src; do
    [ -n "$id" ] || continue
    if [ "$summary" = Organization ]; then
      api "orgs/${src:-$ORG}/rulesets/$id" || continue
    else
      api "repos/$REPO/rulesets/$id" || continue
    fi
    detail="$API_BODY"
    ok="$(jq -r --arg pat "refs/tags/$TAG_PATTERN" '
      def covers: ((.conditions.ref_name.include // []) | any(. == $pat or . == "~ALL" or . == "refs/tags/**" or . == "refs/tags/*"))
                  and (((.conditions.ref_name.exclude // []) | any(. == $pat)) | not);
      ([.rules[].type] ) as $t
      | if (.enforcement == "active") and covers
           and ($t | index("creation")) and ($t | index("update"))
           and ($t | index("deletion")) and ($t | index("non_fast_forward"))
        then "yes" else "no" end' <<<"$detail")"
    if [ "$ok" = yes ]; then found=1; best="$detail"; break; fi
  done <<<"$ids"
  if [ "$found" -eq 0 ]; then
    report FAIL tag-ruleset "no active tag ruleset covers refs/tags/$TAG_PATTERN with creation, update, deletion and non_fast_forward rules"
    return
  fi
  # Organization admins may bypass the tag ruleset on purpose: that use is
  # recorded in the organization audit log. Any other actor is a failure.
  local name actors bad
  name="$(jq -r '.name' <<<"$best")"
  actors="$(jq -r '[.bypass_actors[]? | "\(.actor_type)#\(.actor_id // "-")(\(.bypass_mode // "always"))"] | if length == 0 then "none" else join(", ") end' <<<"$best")"
  if [ -z "$TEAM_ID" ]; then
    report MANUAL tag-ruleset "ruleset '$name' has the required rules; bypass actors: $actors; the id of team $RELEASE_TEAM is not readable, so verify the bypass list by hand"
    return
  fi
  bad="$(jq -r --argjson tid "$TEAM_ID" '[.bypass_actors[]? | select((.actor_type == "Team" and .actor_id == $tid) or .actor_type == "OrganizationAdmin" | not) | "\(.actor_type)#\(.actor_id // "-")"] | join(", ")' <<<"$best")"
  if ! jq -e --argjson tid "$TEAM_ID" 'any(.bypass_actors[]?; .actor_type == "Team" and .actor_id == $tid)' <<<"$best" >/dev/null; then
    report FAIL tag-ruleset "ruleset '$name' bypass actors: $actors; $RELEASE_TEAM is not among them"
  elif [ -n "$bad" ]; then
    report FAIL tag-ruleset "ruleset '$name' bypass is broader than $RELEASE_TEAM and org admins: $bad (all: $actors)"
  else
    report PASS tag-ruleset "ruleset '$name' is active on refs/tags/$TAG_PATTERN with creation, update, deletion, non_fast_forward; bypass actors: $actors"
  fi
}

# --- 2 ---------------------------------------------------------------------
check_environment() {
  local env pols problems=""
  if ! api "repos/$REPO/environments/$ENVIRONMENT"; then
    report FAIL environment "environment $ENVIRONMENT not readable (HTTP $API_STATUS)"
    return
  fi
  env="$API_BODY"
  jq -e --arg t "$APPROVERS_TEAM" 'any(.protection_rules[]?; .type == "required_reviewers" and any(.reviewers[]?; .type == "Team" and (.reviewer.slug == $t or .reviewer.name == $t)))' <<<"$env" >/dev/null \
    || problems="$problems required reviewers do not include team $APPROVERS_TEAM;"
  jq -e 'any(.protection_rules[]?; .type == "required_reviewers" and .prevent_self_review == true)' <<<"$env" >/dev/null \
    || problems="$problems prevent_self_review is not enabled;"
  jq -e '.can_admins_bypass == false' <<<"$env" >/dev/null \
    || problems="$problems administrators can bypass the environment protection rules (can_admins_bypass is not false);"
  if jq -e '.deployment_branch_policy.custom_branch_policies == true' <<<"$env" >/dev/null; then
    if api "repos/$REPO/environments/$ENVIRONMENT/deployment-branch-policies"; then pols="$API_BODY"
      jq -e --arg p "$TAG_PATTERN" '(.branch_policies | length) > 0 and all(.branch_policies[]; .type == "tag" and .name == $p)' <<<"$pols" >/dev/null \
        || problems="$problems deployment policies are not restricted to tags $TAG_PATTERN ($(jq -r '[.branch_policies[]? | "\(.type):\(.name)"] | join(",")' <<<"$pols"));"
    else
      problems="$problems cannot read deployment policies (HTTP $API_STATUS);"
    fi
  else
    problems="$problems no custom deployment policy;"
  fi
  if [ -z "$problems" ]; then
    report PASS environment "$ENVIRONMENT requires $APPROVERS_TEAM, prevents self review, admins cannot bypass, deploys only from tags $TAG_PATTERN"
  else
    report FAIL environment "$ENVIRONMENT:${problems%;}"
  fi
}

# --- 3 ---------------------------------------------------------------------
# Only rulesets are inspected (rules/branches returns ruleset rules); classic
# branch protection is not read and is reported as missing rules.
check_branch_rules() {
  local branch rules refs problems rid rsrc rbody bypass branches=()
  if api "repos/$REPO/git/matching-refs/heads/release/" paginate; then refs="$API_BODY"
    while IFS= read -r branch; do
      [ -n "$branch" ] && branches+=("$branch")
    done < <(jq -r '.[].ref | sub("^refs/heads/"; "")' <<<"$refs" | sort -V)
  fi
  [ "${#branches[@]}" -gt 0 ] || report MANUAL branch-rules "no release/* branch found to check"
  for branch in develop "${branches[@]}"; do
    if ! api "repos/$REPO/rules/branches/$branch" paginate; then
      report MANUAL "branch-rules[$branch]" "cannot read effective rules (HTTP $API_STATUS)"
      continue
    fi
    rules="$API_BODY"
    problems=""
    jq -e 'any(.[]; .type == "pull_request" and (.parameters.required_approving_review_count // 0) >= 1)' <<<"$rules" >/dev/null \
      || problems="$problems pull_request with at least 1 approval missing (found $(jq -r '[.[] | select(.type == "pull_request") | .parameters.required_approving_review_count] | if length == 0 then "no pull_request rule" else join(",") end' <<<"$rules"));"
    jq -e 'any(.[]; .type == "deletion")' <<<"$rules" >/dev/null || problems="$problems deletion not forbidden;"
    jq -e 'any(.[]; .type == "non_fast_forward")' <<<"$rules" >/dev/null || problems="$problems force push not forbidden;"
    # Bypass actors of every ruleset that contributes a rule to this branch.
    while IFS=$'\t' read -r rid rsrc; do
      [ -n "$rid" ] || continue
      if [ "$rsrc" = Organization ]; then
        api "orgs/$ORG/rulesets/$rid" || { problems="$problems cannot read bypass actors of ruleset $rid (HTTP $API_STATUS);"; continue; }
      else
        api "repos/$REPO/rulesets/$rid" || { problems="$problems cannot read bypass actors of ruleset $rid (HTTP $API_STATUS);"; continue; }
      fi
      rbody="$API_BODY"
      bypass="$(jq -r '[.bypass_actors[]? | "\(.actor_type)#\(.actor_id // "-")"] | join(", ")' <<<"$rbody")"
      [ -z "$bypass" ] || problems="$problems ruleset '$(jq -r '.name' <<<"$rbody")' has bypass actors: $bypass;"
    done < <(jq -r '[.[] | select(.ruleset_id != null) | "\(.ruleset_id)\t\(.ruleset_source_type // "Repository")"] | unique | .[]' <<<"$rules")
    if [ -z "$problems" ]; then
      report PASS "branch-rules[$branch]" "pull_request with at least 1 approval, deletion and force push forbidden, no bypass actors"
    else
      problems="${problems# }"; report FAIL "branch-rules[$branch]" "${problems%;}"
    fi
  done
}

# --- 5 ---------------------------------------------------------------------
check_package() {
  local name pkg extra=""
  name="${REPO#*/}"
  if api "orgs/$ORG/packages/container/$name"; then pkg="$API_BODY"
    extra=" (visibility: $(jq -r '.visibility' <<<"$pkg"), linked repository: $(jq -r '.repository.full_name // "none"' <<<"$pkg"))"
  else
    extra=" (package not readable, HTTP $API_STATUS; the token may lack read:packages)"
  fi
  report MANUAL ghcr-package-acl "the API does not expose package Actions access or inheritance; check https://github.com/orgs/$ORG/packages/container/$name/settings -> Manage Actions access: only $REPO with role Write, and 'Inherit access from source repository' as intended$extra"
}


check_teams
check_tag_ruleset
check_environment
check_branch_rules
check_package

if [ "$JSON" -eq 1 ]; then
  printf '%s\n' "${RESULTS[@]}" | jq -s '.'
fi
exit "$FAILED"
