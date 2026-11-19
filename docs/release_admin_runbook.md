# Release settings runbook

This page is for organization owners and repository admins. It describes the GitHub settings that protect the release process and how to confirm them. For how the images are published and verified, see [Verifying release images](./release_verification.md).

## Purpose

A production image must not be publishable by a single account. The release workflow (`.github/workflows/release.yml`) only runs for a `v*` tag and has four jobs, in this order:

1. `preflight` checks that the tagged commit is on `develop` or a `release/*` branch and that the version is not published yet.
2. `build` builds the per-platform images (with SBOM and build provenance) and pushes them by digest to the staging package `ghcr.io/agglayer/aggkit-dev`. It writes nothing to the production package `ghcr.io/agglayer/aggkit`. Its job summaries list the staged digests.
3. `approve` waits for a reviewer other than the person who started the run to approve the `release` environment, so the reviewer decides on images that were actually built. After the approval it prints the tag, the commit, the version and the staged digests.
4. `publish` runs only after the approval. It creates the tagged manifest lists in `ghcr.io/agglayer/aggkit` from the approved staged digests of the same run (the digests come from the build job artifacts, not from a tag lookup), then signs them and creates the attestations. It is the only job with `id-token` and `attestations` write permissions.

Nothing reaches `aggkit` before the approval. These checks only hold if the settings below are in place: otherwise one account could move a tag, change a protected branch, or push to the packages directly.

## Prerequisites

- Organization owner: create teams, org-level rulesets, manage package settings.
- Repository admin on `agglayer/aggkit`: environments, rulesets, branch rules.
- Package admin on the `aggkit` and `aggkit-dev` container packages.
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
- Bypass list: the team `release-manager`. Organization admins may also bypass this ruleset (as "Always"): this is accepted on purpose, and every bypass is recorded in the organization audit log, which should be reviewed. The settings check fails for any other bypass actor.

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

UI: Repository -> Settings -> Rules -> Rulesets -> New branch ruleset, targeting `develop` and `release/**`. The settings check only reads rulesets, not classic branch protection rules, so use rulesets here: a repository protected only by classic branch protection is reported as failing.

- Require a pull request before merging, with at least 1 approving review.
- No direct pushes (do not add bypass actors; the settings check fails when a ruleset that applies to these branches has any).
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

Confirm: `gh api repos/agglayer/aggkit/rules/branches/develop --jq '.[].type'` lists `pull_request`, `deletion` and `non_fast_forward`; repeat with a `release/...` branch. The settings check covers `develop` and every `release/*` branch.

### 5. Restrict the `aggkit` package

UI: Organization -> Packages -> `aggkit` -> Package settings.

- Manage Actions access: only `agglayer/aggkit` with role Write.
- Disable "Inherit access from source repository" (so repository roles do not grant push to the package).
- Visibility: keep the package public. The `preflight` job checks whether a version already exists without logging in, so a private or internal package makes every release fail there.
- Manage access: remove any individual user or team that has Write or Admin, except the package admins.

The API does not expose the Actions access list or inheritance, so this step is checked by hand. Confirm by reading the settings page and by listing direct grants: `gh api orgs/agglayer/packages/container/aggkit --jq '{visibility, repository: .repository.full_name}'`.

Note what the workflow permissions mean for this package. The `build` job has `packages: write`, because it has to push to `aggkit-dev`; `GITHUB_TOKEN` permissions are not scoped per package, so the token of `build` can also write to any package that grants the repository Actions write, `aggkit` included. What keeps `build` out of `aggkit` is what its steps do: they push only to `ghcr.io/agglayer/aggkit-dev`, and the job never creates a tag in `aggkit`. The reviewers of the release approve the images that `build` staged; `publish` is the only job that writes to `aggkit`. See the residual risks for the optional hardening that removes the token-level write access.

### 6. Configure `aggkit-dev` before the first release

Release builds push their per-platform images to `aggkit-dev`, so the package settings must exist and be right before the first release is tagged. The package is created the first time an image is pushed to it, for example by running `Build and Push Docker Image` on a branch once. After that, open Organization -> Packages -> `aggkit-dev` -> Package settings and set:

- Manage Actions access: `agglayer/aggkit` with role Write. The `build` job needs it to stage the images, and `publish` needs to read the staged images (Write includes it).
- Visibility: as wanted for test images (public or internal).
- Manage access: do not grant individual users or other teams Write beyond the package admins, and do not add other repositories to Actions access. `publish` promotes the digests recorded by the `build` job of the same run, so nothing that someone else pushes to `aggkit-dev` is promoted, but keeping the package tight limits pollution and confusion.
- Do not enable a cleanup policy that deletes untagged versions: the staged release images are untagged digests, and they must still exist when the approval is given and `publish` runs.

`aggkit-dev` is not a release channel and nothing in it is signed as a release. It holds branch builds and the staged, not yet approved, release images. The `Build and Push Docker Image` workflow keeps writing branch builds there with the same permission.

### 7. Run the settings check

From a checkout of the repository, with `gh` and `jq` available and enough access to read the settings:

```bash
scripts/release/check_release_settings.sh
```

Optional: `--json` prints the results as JSON; the environment variables `REPO`, `ORG`, `APPROVERS_TEAM`, `RELEASE_TEAM` and `ENVIRONMENT` override the defaults (`agglayer/aggkit`, `agglayer`, `release-approvers`, `release-manager`, `release`).

Expected: every line is `PASS`, except the package control, which prints `MANUAL` and must be confirmed by hand as in step 5. The script exits non-zero when any control prints `FAIL`. The script only reads settings; it changes nothing.

### 8. Dry run with a release candidate

Before the first real release, push a pre-release tag (for example `v0.0.0-rc.1`, from a commit on `develop` or a `release/*` branch) and check each control:

1. The `Release` run finishes `preflight` and `build`, and then waits on the `approve` job for review. The `build` job summaries show the staged image digests in `ghcr.io/agglayer/aggkit-dev`, and nothing exists yet in `ghcr.io/agglayer/aggkit` for the version (`docker buildx imagetools inspect ghcr.io/agglayer/aggkit:<version>` reports not found).
2. The person who pushed the tag cannot approve the run, and a user outside `release-approvers` cannot either.
3. A `v*` tag on a commit that is not on `develop` or a `release/*` branch fails in `preflight` and never reaches `build` or `approve`.
4. After approval, `approve` prints the tag, the commit, the version and the same digests, and `publish` creates the tags in `aggkit`. The platform image digests inside the published manifest list match the staged digests.
5. After a green run, verify the result as described in [Verifying release images](./release_verification.md): `cosign verify` succeeds for the new tags, and `gh attestation verify` succeeds.

Tag names are immutable once published, so use a throwaway version for the dry run and do not reuse it.

## When publishing fails part way

If `preflight` or `build` fails, nothing was written to `aggkit`; only untagged staged digests may remain in `aggkit-dev`, and a re-run after fixing the cause is safe. If a run is not approved or is rejected, the staged images stay in `aggkit-dev` unreferenced and are not published. The staged digests that `publish` needs are kept as run artifacts for 30 days, which is also the longest a run can wait for approval; a run approved or re-run after the artifacts expired fails in `publish` without writing anything, so tag a new version instead.

The `publish` job pushes the manifest lists first and then signs them and creates the attestation. If signing or the attestation fails after the manifests were pushed, the version is already visible in the registry but is not signed. Do not trust or use an unsigned published version. Either use "Re-run failed jobs" on the run, which skips `preflight`, `build` and `approve` and finishes the publication with the same staged digests, or cut a new version. A full re-run of the whole workflow fails in `preflight` because the version is already published.

Besides the version tags, the package also lists signature and attestation artifacts (tags such as `sha256-<digest>.sig` and referrers of the images). These are expected and are not extra releases.

## Residual risks

- Two colluding maintainers (one starting the run, one in `release-approvers`) can still publish an image. Keep the approver team small and review membership regularly.
- Any workflow in the repository that has `packages: write` can push to the packages while they grant Actions write to the repository. This includes the `build` job of the release workflow, which writes to `aggkit-dev` and, because the token is not scoped per package, could write to `aggkit` if its steps were changed. Review workflow changes with that in mind. Optional hardening: use a push credential for `aggkit` stored only as a `release` environment secret, and have `aggkit` grant no Actions write access to the repository, so only jobs that passed the environment approval can push to it.
- A staged image can be tampered with only in a bounded way. `publish` promotes the exact digests recorded by the `build` job of the same run, and a digest is the hash of the content, so a different image pushed to `aggkit-dev` cannot take their place and is never promoted. Anyone with write access to `aggkit-dev` can still add or delete versions there (for example delete a staged image, which makes `publish` fail), but cannot change what is promoted. For that reason `aggkit-dev` should grant write access only to the repository and the package admins, but it is not part of the trust base of a published image.
- The reviewer sees the staged digests in the `build` job summaries before approving, not a human-readable review of the code in the images. The approval confirms the tag, the commit and that the build succeeded, including the version check inside the image.
- The ancestry check in `preflight` relies on steps 3 and 4. Tag-triggered runs use the workflow file from the tagged commit, so someone who can push a tag could also push a modified workflow. Protecting tags (step 3) and the branches the tags must come from (step 4) is what keeps this check meaningful.
