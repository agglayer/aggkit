# Release settings runbook

This page is for organization owners and repository admins. It describes the GitHub settings that protect the release process and how to confirm them. For how the images are published and verified, see [Verifying release images](./release_verification.md).

## Purpose

A production image must not be publishable by a single account. The release workflow (`.github/workflows/release.yml`) only runs for a `v*` tag, checks that the tagged commit is on `develop` or a `release/*` branch, and then waits for a reviewer other than the person who started it to approve the `release` environment before anything is pushed. These checks only hold if the settings below are in place: otherwise one account could move a tag, change a protected branch, or push to the package directly.

## Prerequisites

- Organization owner: create teams, org-level rulesets, manage package settings.
- Repository admin on `agglayer/aggkit`: environments, rulesets, branch rules.
- Package admin on the `aggkit` container package.
- GitHub CLI (`gh`) logged in with `repo`, `admin:org` and `read:packages` scopes for the `gh api` commands.

Names used below: org `agglayer`, repository `agglayer/aggkit`, teams `release-approvers` and `release-manager`, environment `release`.

## Steps

### 1. Create the teams

UI: Organization -> Teams -> New team.

- `release-approvers`: people who may approve a release. Keep it small, and do not add everyone who can push tags.
- `release-manager`: people who may bypass the tag ruleset (for example to delete a tag created by mistake). Keep it even smaller.

```bash
gh api -X POST orgs/agglayer/teams -f name=release-approvers -f privacy=closed
gh api -X POST orgs/agglayer/teams -f name=release-manager -f privacy=closed
gh api -X PUT orgs/agglayer/teams/release-approvers/repos/agglayer/aggkit -f permission=push
```

Confirm: `gh api orgs/agglayer/teams/release-approvers --jq .slug` and the same for `release-manager` print the slug.

### 2. Configure the `release` environment

UI: Repository -> Settings -> Environments -> `release`.

- Required reviewers: team `release-approvers`.
- Prevent self-review: on (the person who triggered the run cannot approve it).
- Allow administrators to bypass: off.
- Deployment branches and tags: "Selected branches and tags", with one rule of type tag, pattern `v*`.

```bash
APPROVERS_ID=$(gh api orgs/agglayer/teams/release-approvers --jq .id)
gh api -X PUT repos/agglayer/aggkit/environments/release --input - <<JSON
{
  "reviewers": [{"type": "Team", "id": $APPROVERS_ID}],
  "prevent_self_review": true,
  "can_admins_bypass": false,
  "deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true}
}
JSON
gh api -X POST repos/agglayer/aggkit/environments/release/deployment-branch-policies \
  -f name='v*' -f type=tag
```

Remove any pre-existing branch policy on the environment: only the tag policy `v*` should remain.

Confirm:

```bash
gh api repos/agglayer/aggkit/environments/release
gh api repos/agglayer/aggkit/environments/release/deployment-branch-policies
```

The first shows the `required_reviewers` rule with the team and `prevent_self_review: true`; the second lists only `{"type":"tag","name":"v*"}`.

### 3. Protect release tags with a ruleset

UI: Repository (or Organization) -> Settings -> Rules -> Rulesets -> New tag ruleset.

- Enforcement: Active. Target: tags matching `refs/tags/v*` (include pattern `v*`).
- Rules: restrict creations, restrict updates, restrict deletions, block force pushes (non-fast-forward).
- Bypass list: only the team `release-manager`; organization admins may be added as "Always" only if their use is audited (the audit log records ruleset bypasses).

```bash
RELEASE_MANAGER_ID=$(gh api orgs/agglayer/teams/release-manager --jq .id)
gh api -X POST repos/agglayer/aggkit/rulesets --input - <<JSON
{
  "name": "release-tags",
  "target": "tag",
  "enforcement": "active",
  "conditions": {"ref_name": {"include": ["refs/tags/v*"], "exclude": []}},
  "rules": [{"type": "creation"}, {"type": "update"}, {"type": "deletion"}, {"type": "non_fast_forward"}],
  "bypass_actors": [{"actor_id": $RELEASE_MANAGER_ID, "actor_type": "Team", "bypass_mode": "always"}]
}
JSON
```

For an organization-level ruleset, post to `orgs/agglayer/rulesets` and add `"repository_name": {"include": ["aggkit"], "exclude": []}` to `conditions`.

Confirm: `gh api repos/agglayer/aggkit/rulesets --jq '.[] | {name, target, enforcement}'` lists the ruleset as active. Then check that creating a `v*` tag as a normal maintainer is refused: `git push origin test-tag:refs/tags/v0.0.0-check` must be rejected (delete nothing if it is not).

### 4. Protect `develop` and `release/*`

UI: Repository -> Settings -> Rules -> Rulesets -> New branch ruleset, targeting `develop` and `release/**` (or the equivalent classic branch protection rules).

- Require a pull request before merging, with at least 1 approving review.
- No direct pushes (do not add bypass actors).
- Restrict deletions and block force pushes.

```bash
gh api -X POST repos/agglayer/aggkit/rulesets --input - <<'JSON'
{
  "name": "protected-branches",
  "target": "branch",
  "enforcement": "active",
  "conditions": {"ref_name": {"include": ["refs/heads/develop", "refs/heads/release/**"], "exclude": []}},
  "rules": [
    {"type": "pull_request", "parameters": {"required_approving_review_count": 1, "dismiss_stale_reviews_on_push": true, "require_code_owner_review": false, "require_last_push_approval": false, "required_review_thread_resolution": false}},
    {"type": "deletion"},
    {"type": "non_fast_forward"}
  ],
  "bypass_actors": []
}
JSON
```

Confirm: `gh api repos/agglayer/aggkit/rules/branches/develop --jq '.[].type'` lists `pull_request`, `deletion` and `non_fast_forward`; repeat with a `release/...` branch.

### 5. Restrict the `aggkit` package

UI: Organization -> Packages -> `aggkit` -> Package settings.

- Manage Actions access: only `agglayer/aggkit` with role Write.
- Disable "Inherit access from source repository" (so repository roles do not grant push to the package).
- Manage access: remove any individual user or team that has Write or Admin, except the package admins.

The API does not expose the Actions access list or inheritance, so this step is checked by hand. Confirm by reading the settings page and by listing direct grants: `gh api orgs/agglayer/packages/container/aggkit --jq '{visibility, repository: .repository.full_name}'`.

### 6. Configure `aggkit-dev` after the first branch build

The `aggkit-dev` package is created the first time `Build and Push Docker Image` runs for a branch. After that run, open Organization -> Packages -> `aggkit-dev` -> Package settings and set:

- Visibility: as wanted for test images (public or internal).
- Manage Actions access: `agglayer/aggkit` with role Write.

`aggkit-dev` is not a release channel and nothing in it is signed as a release.

### 7. Run the settings check

From a checkout of the repository, with `gh` and `jq` available and enough access to read the settings:

```bash
scripts/release/check_release_settings.sh
```

Optional: `--json` prints the results as JSON; the environment variables `REPO`, `ORG`, `APPROVERS_TEAM`, `RELEASE_TEAM` and `ENVIRONMENT` override the defaults (`agglayer/aggkit`, `agglayer`, `release-approvers`, `release-manager`, `release`).

Expected: every line is `PASS`, except the package control, which prints `MANUAL` and must be confirmed by hand as in step 5. The script exits non-zero when any control prints `FAIL`. The script only reads settings; it changes nothing.

### 8. Dry run with a release candidate

Before the first real release, push a pre-release tag (for example `v0.0.0-rc.1`, from a commit on `develop` or a `release/*` branch) and check each control:

1. The `Release` run finishes `preflight` and then waits on the `approve` job for review.
2. The person who pushed the tag cannot approve the run, and a user outside `release-approvers` cannot either.
3. A `v*` tag on a commit that is not on `develop` or a `release/*` branch fails in `preflight` and never reaches `approve`.
4. After approval and a green run, verify the result as described in [Verifying release images](./release_verification.md): `cosign verify` succeeds for the new tags, and `gh attestation verify` succeeds.

Tag names are immutable once published, so use a throwaway version for the dry run and do not reuse it.

## Residual risks

- Two colluding maintainers (one starting the run, one in `release-approvers`) can still publish an image. Keep the approver team small and review membership regularly.
- Any workflow in the repository that has `packages: write` can push to the package while the package grants Actions write to the repository. Review workflow changes with that in mind. Optional hardening: use a push credential stored only as a `release` environment secret, and have the package grant no Actions write access to the repository, so only jobs that passed the environment approval can push.
- The ancestry check in `preflight` relies on steps 3 and 4. Tag-triggered runs use the workflow file from the tagged commit, so someone who can push a tag could also push a modified workflow. Protecting tags (step 3) and the branches the tags must come from (step 4) is what keeps this check meaningful.
