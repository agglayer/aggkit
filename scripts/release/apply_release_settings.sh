#!/usr/bin/env bash
# Applies the repository settings that gate releases, idempotently.
# Without --apply it is a dry run: it reads the live settings (GET calls only),
# prints the change set as the `gh api` calls it would make, with a diff against
# the live state, and changes nothing.
#
# What it manages (repository level, through the REST API):
#   - the tag ruleset: creation, update, deletion and force pushes on the release
#     tags are limited to the release team (the only bypass actor, besides the
#     organization admins already listed in an existing ruleset)
#   - branch rules for develop, release/* (and release/*/* for components): a pull
#     request with at least one approval, no deletion, no force push. They are
#     added to the existing branch ruleset, never as a duplicate
#   - the release environments: required reviewers, prevent self review, no admin
#     bypass, and deployment only from the release tag pattern
# It never changes organization teams, packages, secrets or tokens: for those it
# prints the request text to hand to whoever owns them.
#
# Usage: apply_release_settings.sh [--repo owner/name] [--components list] [--apply]
#   --repo        repository to configure (default agglayer/aggkit)
#   --components  comma separated list of: legacy, aggkit, ger_validator_signer,
#                 sdk, dev_ui (default legacy)
#                   legacy  environment `release`, tags `v*`
#                   <name>  environment `release-<name>`, tags `<name>/v*`
#                 The ruleset always covers `refs/tags/v*`; `refs/tags/*/v*` is
#                 added as soon as a component other than legacy is listed.
#   --apply       make the changes (default: dry run)
# Env:   APPROVERS_TEAM (release-approvers), RELEASE_TEAM (release-manager),
#        TAG_RULESET_NAME (release-tags), BRANCH_RULESET_NAME (Merge only PR ruleset)
# Exit:  0 when the live state already matches or the changes were made (dry run:
#        always 0 unless a read fails); 1 when a call failed or a dependency
#        (a team) is missing so that part could not be applied; 2 on bad usage.
# Afterwards run check_release_settings.sh to confirm the result.
set -uo pipefail

REPO="agglayer/aggkit"
COMPONENTS="legacy"
APPLY=0
APPROVERS_TEAM="${APPROVERS_TEAM:-release-approvers}"
RELEASE_TEAM="${RELEASE_TEAM:-release-manager}"
TAG_RULESET_NAME="${TAG_RULESET_NAME:-release-tags}"
BRANCH_RULESET_NAME="${BRANCH_RULESET_NAME:-Merge only PR ruleset}"
KNOWN_COMPONENTS="legacy aggkit ger_validator_signer sdk dev_ui"

usage() { sed -n '/^# Usage:/,/^# Afterwards/p' "$0" | sed 's/^# \{0,1\}//' >&2; }

while [ $# -gt 0 ]; do
  case "$1" in
    --repo) [ $# -ge 2 ] || { usage; exit 2; }; REPO="$2"; shift 2 ;;
    --repo=*) REPO="${1#--repo=}"; shift ;;
    --components) [ $# -ge 2 ] || { usage; exit 2; }; COMPONENTS="$2"; shift 2 ;;
    --components=*) COMPONENTS="${1#--components=}"; shift ;;
    --apply) APPLY=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

case "$REPO" in */*) ;; *) echo "--repo must be owner/name, got: $REPO" >&2; exit 2 ;; esac
ORG="${ORG:-${REPO%%/*}}"

for tool in gh jq diff; do
  command -v "$tool" >/dev/null 2>&1 || { echo "missing required tool: $tool" >&2; exit 2; }
done

TARGETS=()
HAS_COMPONENTS=0
IFS=',' read -r -a _requested <<<"$COMPONENTS"
for c in "${_requested[@]}"; do
  c="${c// /}"
  [ -n "$c" ] || continue
  case " $KNOWN_COMPONENTS " in
    *" $c "*) ;;
    *) echo "unknown component: $c (known: $KNOWN_COMPONENTS)" >&2; exit 2 ;;
  esac
  [ "$c" = legacy ] || HAS_COMPONENTS=1
  TARGETS+=("$c")
done
[ "${#TARGETS[@]}" -gt 0 ] || { echo "--components needs at least one value" >&2; exit 2; }

CHANGES=0
FAILED=0
SKIPPED=0
MODE="dry run"
[ "$APPLY" -eq 1 ] && MODE="apply"

section() { printf '\n== %s ==\n' "$1"; }
info() { printf '%s\n' "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; FAILED=1; }

API_STATUS=""
API_BODY=""
# api PATH [paginate]: GET. On success stores the body in API_BODY; otherwise
# sets API_STATUS to the HTTP status (or "error") and returns 1. With a second
# argument the endpoint is a JSON array fetched page by page and merged.
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

# mutate METHOD PATH [BODY]: prints the call; makes it only with --apply.
mutate() {
  local method="$1" path="$2" body="${3:-}"
  CHANGES=$((CHANGES + 1))
  if [ "$APPLY" -eq 1 ]; then printf '  run: '; else printf '  would run: '; fi
  printf 'gh api -X %s %s%s\n' "$method" "$path" "${body:+ --input -}"
  if [ -n "$body" ]; then jq . <<<"$body" | sed 's/^/        /'; fi
  [ "$APPLY" -eq 1 ] || return 0
  if [ -n "$body" ]; then
    printf '%s' "$body" | gh api -X "$method" "$path" --input - >/dev/null || fail "$method $path failed"
  else
    gh api -X "$method" "$path" >/dev/null || fail "$method $path failed"
  fi
}

show_diff() { # live desired
  diff -u --label live --label desired <(jq -S . <<<"$1") <(jq -S . <<<"$2") | tail -n +3 | sed 's/^/    /' || true
}

# --- teams -------------------------------------------------------------------
# A team id is a JSON number, or a quoted placeholder when the team cannot be
# read (the dry run then still prints the full change set).
NEEDS_TEAM_ACCESS=0
APPROVERS_ID=""
RELEASE_ID=""
MISSING_TEAMS=""
resolve_teams() {
  section "Teams"
  if api "orgs/$ORG/teams/$RELEASE_TEAM"; then RELEASE_ID="$(jq -c '.id' <<<"$API_BODY")"; info "ok    team $RELEASE_TEAM (id $RELEASE_ID)"
  else RELEASE_ID="\"<id of team $RELEASE_TEAM>\""; MISSING_TEAMS="$MISSING_TEAMS $RELEASE_TEAM"
    info "MISSING team $RELEASE_TEAM (HTTP $API_STATUS); the API also answers this for a team the token cannot see"; fi
  if api "orgs/$ORG/teams/$APPROVERS_TEAM"; then APPROVERS_ID="$(jq -c '.id' <<<"$API_BODY")"; info "ok    team $APPROVERS_TEAM (id $APPROVERS_ID)"
  else APPROVERS_ID="\"<id of team $APPROVERS_TEAM>\""; MISSING_TEAMS="$MISSING_TEAMS $APPROVERS_TEAM"
    info "MISSING team $APPROVERS_TEAM (HTTP $API_STATUS)"; fi
  if [ -n "$MISSING_TEAMS" ]; then
    info "The tag ruleset and the environments need the teams: they are shown below with placeholder ids"
    [ "$APPLY" -eq 0 ] || info "and are NOT applied until the teams exist. Branch rules do not need them."
  fi
  if [[ "$APPROVERS_ID" =~ ^[0-9]+$ ]] && ! api "orgs/$ORG/teams/$APPROVERS_TEAM/repos/$REPO"; then
    info "note  team $APPROVERS_TEAM has no access to $REPO; reviewers must have at least read access (see the requests below)"
    NEEDS_TEAM_ACCESS=1
  fi
}

# --- rulesets ------------------------------------------------------------------
# norm: the fields that matter, in a canonical order, so two rulesets compare equal.
NORM='{name, target, enforcement,
  conditions: {ref_name: {include: ((.conditions.ref_name.include // []) | sort), exclude: ((.conditions.ref_name.exclude // []) | sort)}},
  rules: ([.rules[]? | {type} + (if .parameters then {parameters} else {} end)] | sort_by(.type)),
  bypass_actors: ([.bypass_actors[]? | {actor_id, actor_type, bypass_mode: (.bypass_mode // "always")}] | sort_by(.actor_type, (.actor_id // 0)))}'

# body: what is sent. Rules without parameters carry none.
BODY='{name, target, enforcement,
  conditions: {ref_name: {include: ((.conditions.ref_name.include // []) | unique), exclude: (.conditions.ref_name.exclude // [])}},
  rules: [.rules[]? | if .parameters then {type, parameters} else {type} end],
  bypass_actors: [.bypass_actors[]? | {actor_id, actor_type, bypass_mode: (.bypass_mode // "always")}]}'

# find_ruleset NAME TARGET: stores the detail of the repository ruleset in
# RULESET_LIVE ("" when absent). Returns 1 when the ruleset list cannot be read.
RULESET_LIVE=""
RULESET_ID=""
find_ruleset() {
  RULESET_LIVE=""; RULESET_ID=""
  if ! api "repos/$REPO/rulesets?includes_parents=false" paginate; then
    fail "cannot list rulesets of $REPO (HTTP $API_STATUS); repository admin access is needed"
    return 1
  fi
  RULESET_ID="$(jq -r --arg n "$1" --arg t "$2" 'first(.[] | select(.name == $n and .target == $t and ((.source_type // "Repository") == "Repository")) | .id) // empty' <<<"$API_BODY")"
  [ -n "$RULESET_ID" ] || return 0
  if ! api "repos/$REPO/rulesets/$RULESET_ID"; then
    fail "cannot read ruleset $RULESET_ID of $REPO (HTTP $API_STATUS)"
    RULESET_ID=""
    return 1
  fi
  RULESET_LIVE="$API_BODY"
}

# upsert_ruleset LABEL DESIRED_NORMALISED_BODY: creates or updates RULESET_ID.
upsert_ruleset() {
  local label="$1" desired="$2" body
  body="$(jq -c "$BODY" <<<"$desired")"
  if [ -z "$RULESET_LIVE" ]; then
    info "CREATE $label"
    mutate POST "repos/$REPO/rulesets" "$body"
  elif [ "$(jq -cS "$NORM" <<<"$RULESET_LIVE")" = "$(jq -cS "$NORM" <<<"$desired")" ]; then
    info "ok     $label is up to date"
  else
    info "UPDATE $label (ruleset $RULESET_ID)"
    show_diff "$(jq "$NORM" <<<"$RULESET_LIVE")" "$(jq "$NORM" <<<"$desired")"
    mutate PUT "repos/$REPO/rulesets/$RULESET_ID" "$body"
  fi
}

ensure_tag_ruleset() {
  section "Tag ruleset '$TAG_RULESET_NAME'"
  if [ -n "$MISSING_TEAMS" ] && [ "$APPLY" -eq 1 ]; then info "SKIP   teams are missing"; SKIPPED=1; return; fi
  find_ruleset "$TAG_RULESET_NAME" tag || return
  local include='["refs/tags/v*"]'
  [ "$HAS_COMPONENTS" -eq 0 ] || include='["refs/tags/v*", "refs/tags/*/v*"]'
  local base="$RULESET_LIVE"
  [ -n "$base" ] || base='{"name":"","target":"tag","enforcement":"active","conditions":{"ref_name":{"include":[],"exclude":[]}},"rules":[],"bypass_actors":[]}'
  local desired
  desired="$(jq --arg name "$TAG_RULESET_NAME" --argjson inc "$include" --argjson rid "$RELEASE_ID" '
    .name = $name | .target = "tag" | .enforcement = "active"
    | .conditions.ref_name.include = ((.conditions.ref_name.include // []) + $inc | unique)
    | .conditions.ref_name.exclude = ((.conditions.ref_name.exclude // []) - $inc)
    | .rules = ((.rules // []) as $r
        | [$r[] | select(.type | IN("creation","update","deletion","non_fast_forward") | not)]
        + [{type:"creation"},{type:"update"},{type:"deletion"},{type:"non_fast_forward"}])
    | .bypass_actors = ([{actor_id: $rid, actor_type: "Team", bypass_mode: "always"}]
        + [(.bypass_actors // [])[] | select(.actor_type == "OrganizationAdmin")])' <<<"$base")"
  upsert_ruleset "tag ruleset covering $(jq -r '.conditions.ref_name.include | join(", ")' <<<"$desired")" "$desired"
}

ensure_branch_rules() {
  section "Branch rules in '$BRANCH_RULESET_NAME'"
  find_ruleset "$BRANCH_RULESET_NAME" branch || return
  local default_branch inc='["refs/heads/release/*"]'
  if api "repos/$REPO"; then default_branch="$(jq -r '.default_branch' <<<"$API_BODY")"; else default_branch=develop; fi
  [ "$HAS_COMPONENTS" -eq 0 ] || inc='["refs/heads/release/*", "refs/heads/release/*/*"]'
  local base="$RULESET_LIVE"
  [ -n "$base" ] || base='{"name":"","target":"branch","enforcement":"active","conditions":{"ref_name":{"include":[],"exclude":[]}},"rules":[],"bypass_actors":[]}'
  local desired
  desired="$(jq --arg name "$BRANCH_RULESET_NAME" --arg defb "$default_branch" --argjson inc "$inc" '
    .name = $name | .target = "branch" | .enforcement = "active"
    | (.conditions.ref_name.include // []) as $have
    | .conditions.ref_name.include =
        ($have + $inc
         + (if $defb == "develop" and ($have | index("~DEFAULT_BRANCH")) then [] else ["refs/heads/develop"] end)
         | unique)
    | .rules = (
        (.rules // [])
        | (if any(.[]; .type == "pull_request")
           then map(if .type == "pull_request" and ((.parameters.required_approving_review_count // 0) < 1)
                    then .parameters.required_approving_review_count = 1 else . end)
           else . + [{type: "pull_request", parameters: {required_approving_review_count: 1,
                dismiss_stale_reviews_on_push: true, require_code_owner_review: false,
                require_last_push_approval: false, required_review_thread_resolution: false}}] end)
        | (if any(.[]; .type == "deletion") then . else . + [{type: "deletion"}] end)
        | (if any(.[]; .type == "non_fast_forward") then . else . + [{type: "non_fast_forward"}] end))' <<<"$base")"
  [ -n "$RULESET_LIVE" ] || info "note   no ruleset named '$BRANCH_RULESET_NAME' exists yet, so one is created"
  upsert_ruleset "branch rules for $(jq -r '.conditions.ref_name.include | join(", ")' <<<"$desired")" "$desired"
}

# --- environments -------------------------------------------------------------
env_state() { # environment JSON (or null) -> comparable state
  jq -cS '{
    reviewers: ([.protection_rules[]? | select(.type == "required_reviewers") | .reviewers[]? | {type, id: .reviewer.id}] | sort_by(.type, .id)),
    prevent_self_review: (any(.protection_rules[]?; .type == "required_reviewers" and .prevent_self_review == true)),
    can_admins_bypass: (if .can_admins_bypass == false then false else true end),
    custom_branch_policies: (.deployment_branch_policy.custom_branch_policies // false)}' <<<"$1"
}

ensure_environment() { # environment-name tag-pattern
  local env="$1" pattern="$2" live="null" desired state_live state_desired body
  section "Environment '$env' (deploys from tags $pattern)"
  if [ -n "$MISSING_TEAMS" ] && [ "$APPLY" -eq 1 ]; then info "SKIP   teams are missing"; SKIPPED=1; return; fi
  if api "repos/$REPO/environments/$env"; then live="$API_BODY"
  elif [ "$API_STATUS" != 404 ]; then fail "cannot read environment $env (HTTP $API_STATUS)"; return
  fi
  desired="$(jq -cn --argjson aid "$APPROVERS_ID" '{
    reviewers: [{type: "Team", id: $aid}], prevent_self_review: true,
    can_admins_bypass: false, custom_branch_policies: true}')"
  state_live="$(env_state "$live")"
  state_desired="$(jq -cS . <<<"$desired")"
  if [ "$live" = null ]; then
    info "CREATE environment $env"
  elif [ "$state_live" = "$state_desired" ]; then
    info "ok     environment protection rules are up to date"
  else
    info "UPDATE environment $env"
    show_diff "$state_live" "$state_desired"
  fi
  if [ "$live" = null ] || [ "$state_live" != "$state_desired" ]; then
    body="$(jq -c '{reviewers, prevent_self_review, can_admins_bypass,
      deployment_branch_policy: {protected_branches: false, custom_branch_policies: true}}' <<<"$desired")"
    mutate PUT "repos/$REPO/environments/$env" "$body"
  fi
  # Deployment policies: exactly one, of type tag with this pattern.
  local policies="[]" present=0 id type name
  if [ "$live" != null ]; then
    if api "repos/$REPO/environments/$env/deployment-branch-policies"; then policies="$(jq -c '.branch_policies // []' <<<"$API_BODY")"
    else fail "cannot read deployment policies of $env (HTTP $API_STATUS)"; return; fi
  fi
  while IFS=$'\t' read -r id type name; do
    [ -n "$id" ] || continue
    if [ "$type" = tag ] && [ "$name" = "$pattern" ] && [ "$present" -eq 0 ]; then present=1
    else info "REMOVE deployment policy $type:$name"
      mutate DELETE "repos/$REPO/environments/$env/deployment-branch-policies/$id"; fi
  done < <(jq -r '.[] | [.id, (.type // "branch"), .name] | @tsv' <<<"$policies")
  if [ "$present" -eq 1 ]; then info "ok     deployment policy tag:$pattern"
  else info "ADD    deployment policy tag:$pattern"
    mutate POST "repos/$REPO/environments/$env/deployment-branch-policies" "$(jq -cn --arg n "$pattern" '{name: $n, type: "tag"}')"
  fi
}

# --- requests for other people -------------------------------------------------
# What this script cannot do. Printed text is meant to be forwarded as is.
print_requests() {
  section "Requests for others (this script never changes these)"
  local c env pkg any=0
  if [ -n "$MISSING_TEAMS" ]; then
    any=1
    cat <<EOF
To the organization owners ($ORG admins):
  Create the teams:${MISSING_TEAMS} in $ORG (secret or closed, as usual) and tell us who the members are:
    release-approvers  people who may approve a release; keep it small
    release-manager    people who may create and delete release tags; keep it smaller
  Give both teams at least read access to $REPO (release-approvers needs it to be a reviewer):
    gh api -X PUT orgs/$ORG/teams/release-approvers/repos/$REPO -f permission=push
    gh api -X PUT orgs/$ORG/teams/release-manager/repos/$REPO -f permission=push
  Then run this script again.

EOF
  elif [ "$NEEDS_TEAM_ACCESS" -eq 1 ]; then
    any=1
    cat <<EOF
To the organization owners ($ORG admins):
  Give the team $APPROVERS_TEAM at least read access to $REPO:
    gh api -X PUT orgs/$ORG/teams/$APPROVERS_TEAM/repos/$REPO -f permission=push

EOF
  fi
  for c in "${TARGETS[@]}"; do
    case "$c" in
      legacy) env=release; pkg="${REPO#*/}" ;;
      aggkit) env=release-aggkit; pkg=aggkit ;;
      ger_validator_signer) env=release-ger_validator_signer; pkg=ger-validator-signer ;;
      dev_ui) env=release-dev_ui; pkg=agglayer-dev-ui ;;
      sdk) env=release-sdk; pkg="" ;;
    esac
    any=1
    if [ -n "$pkg" ]; then
      cat <<EOF
To the admins of the package ghcr.io/$ORG/$pkg ($c):
  Organization -> Packages -> $pkg -> Package settings:
    - turn off "Inherit access from source repository"
    - Manage Actions access: $REPO must not be listed (the repository's workflow token must not write here)
    - Manage access: write only for the package admins and one machine account owned by the organization
  Then, as that machine account, create a personal access token (classic) with the scope write:packages and
  store it only in the environment $env of $REPO (never as a repository or organization secret):
    gh secret set RELEASE_REGISTRY_USER --repo $REPO --env $env --body '<machine account login>'
    gh secret set RELEASE_REGISTRY_TOKEN --repo $REPO --env $env
  The branch-build package ghcr.io/$ORG/$pkg-dev must grant $REPO Actions write.

EOF
    else
      cat <<EOF
To the npm owners of @agglayer/sdk ($c):
  List the current maintainers and the publish tokens that exist, so the extra ones can be revoked. Switching
  to a trusted publisher (repository $REPO, the sdk release workflow, environment $env) is done at the cutover.

EOF
    fi
  done
  [ "$any" -eq 1 ] || info "none"
}

# --- run -----------------------------------------------------------------------
info "Release settings for $REPO ($MODE), components: ${TARGETS[*]}"
[ "$APPLY" -eq 1 ] || info "Nothing is changed in a dry run. Add --apply to make the changes listed below."

resolve_teams
ensure_tag_ruleset
ensure_branch_rules
for c in "${TARGETS[@]}"; do
  if [ "$c" = legacy ]; then ensure_environment release 'v*'
  else ensure_environment "release-$c" "$c/v*"; fi
done
print_requests

section "Summary"
if [ "$APPLY" -eq 1 ]; then
  info "$CHANGES call(s) made."
else
  info "$CHANGES call(s) would be made."
fi
[ "$SKIPPED" -eq 0 ] || { info "Some parts were not applied because a team is missing."; FAILED=1; }
[ "$CHANGES" -ne 0 ] || [ "$FAILED" -ne 0 ] || info "The live settings already match."
info "Afterwards run: scripts/release/check_release_settings.sh"
exit "$FAILED"
