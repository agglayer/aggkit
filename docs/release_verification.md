# Verifying release images

This page is for people who run Aggkit. It explains which images are published, how to check that an image really came from the Aggkit release process, and how to pin it in a deployment.

## Published tags

Release images are published to `ghcr.io/agglayer/aggkit`, one set of tags per release version `X.Y.Z` (for example `1.2.3`, or `1.2.3-rc.1` for a pre-release):

| Tag | Content |
| --- | --- |
| `X.Y.Z` | Production image, no shell. `linux/amd64` and `linux/arm64`. |
| `X.Y.Z-no_shell` | Alias of `X.Y.Z` (same image). |
| `X.Y.Z-dev` | Development image that includes a shell, for debugging. Not recommended for production. |

Only exact versions are published and a published version is never overwritten. The package also lists signature and attestation artifacts next to these tags (for example `sha256-<digest>.sig` tags and referrers of the images); they are not extra versions.

Release images reach `ghcr.io/agglayer/aggkit` only through the release workflow, in this order: the platform images are built and staged in `ghcr.io/agglayer/aggkit-dev` (see below), a reviewer approves them, and only then are the tags above created in `aggkit` from those exact images and signed. A release that fails to build or is not approved never appears in `aggkit`. The credential that can write to `aggkit` is stored only as secrets of the release environment that the reviewer approves; the workflow token of the repository, which builds and stages the images, cannot write to `aggkit`.

If signing or attestation fails after the manifests were pushed, the version is visible but unsigned. Treat an unsigned version as unusable: it is either completed by re-running the failed jobs or replaced by a new version. Always verify the signature before using an image.

**Change:** the moving `X.Y` tags (for example `1.2`, `1.2-no_shell`, `1.2-dev`) and the `latest` tag are no longer published. Pin an exact version. Existing `X.Y` and `latest` tags in the registry are not updated any more, so a deployment that follows them silently stays on an old image.

Each platform image carries an SBOM and build provenance, and each manifest list is signed (see below).

The production tag `X.Y.Z` resolves to a manifest list that contains the production images only. The `-dev` platform images are assembled into their own manifest list under `X.Y.Z-dev`.

### Branch builds

`ghcr.io/agglayer/aggkit-dev` holds images built manually from a branch, tagged with the branch name, a timestamp and the commit. They are for testing only, are not release candidates and are not signed as releases. Do not use them in production.

### Staged release candidates

`aggkit-dev` is also the staging area of the release workflow. While a release is built and waiting for approval, its per-platform images are pushed there by digest, without a tag, next to the branch builds. These images are not published releases: they are unsigned, may belong to a release that is never approved, and can be written to by anyone who has write access to `aggkit-dev`. Use only the tags in `ghcr.io/agglayer/aggkit`, and verify them as described below. The approved images are copied to `aggkit` by digest, so the platform images in a published version are the ones that were built and reviewed in the same run.

### Images published before this change

Images published before signing was introduced are unsigned and have no attestation. `cosign verify` and `gh attestation verify` fail for them. Use a version published after this change when you need verified images.

## Verify the signature

Every release image is signed with [cosign](https://docs.sigstore.dev/cosign/overview/) keyless signing. The signing identity is the release workflow of this repository, running at a `v*` tag:

```bash
cosign verify ghcr.io/agglayer/aggkit:<version> \
  --certificate-identity-regexp '^https://github.com/agglayer/aggkit/.github/workflows/release.yml@refs/tags/v.*$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

A successful run prints the verified claims, including the workflow identity and the tag. Any other identity, issuer or an unsigned image makes the command fail.

## Verify build provenance

Release images also have a GitHub build provenance attestation, stored in the registry next to the image. Verify it with the GitHub CLI:

```bash
gh attestation verify oci://ghcr.io/agglayer/aggkit:<version> \
  --repo agglayer/aggkit \
  --signer-workflow agglayer/aggkit/.github/workflows/release.yml
```

This confirms the image was built by the release workflow of `agglayer/aggkit`.

## Inspect the SBOM

The platform images carry a software bill of materials:

```bash
docker buildx imagetools inspect ghcr.io/agglayer/aggkit:<version> --format '{{ json .SBOM }}'
```

The build provenance of the platform images can be read the same way with `--format '{{ json .Provenance }}'`.

## Pin by digest

Tags can be moved by whoever has push access to the registry; a digest cannot. Resolve the digest of a version you verified and deploy that:

```bash
docker buildx imagetools inspect ghcr.io/agglayer/aggkit:<version> --format '{{json .Manifest}}' | jq -r .digest
```

Then reference the image as `ghcr.io/agglayer/aggkit@sha256:<digest>` (optionally keeping the tag for readability: `aggkit:<version>@sha256:<digest>`). Verify the signature against the digest, not only the tag:

```bash
cosign verify ghcr.io/agglayer/aggkit@sha256:<digest> \
  --certificate-identity-regexp '^https://github.com/agglayer/aggkit/.github/workflows/release.yml@refs/tags/v.*$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Enforce verification in the cluster

Running the commands above by hand only helps if someone remembers to do it. For deployments that matter, enforce the check at admission time, so an image that is unsigned or signed by a different identity is refused. Kubernetes admission controllers such as Sigstore policy-controller, Kyverno or Ratify can require a valid cosign signature with the same identity regexp and issuer shown above, and can require images to be referenced by digest.
