# TEMPORARY: image of agglayer-contracts with the AggOracleCommittee v2, built by the CI only for the committee v2 e2e
# job (.github/workflows/test-e2e.yml). It is never pushed to a registry. Delete it when an image of agglayer-contracts
# with the AggOracleCommittee v2 is published, and set that image in .github/test_e2e_op_aggoracle_committee_v2_args.json.
#
# It has the same tools as kurtosis-cdk docker/agglayer-contracts.Dockerfile. The difference is that the contracts are
# not cloned and compiled inside the build: `npx hardhat compile` fails there with ELOCKED (proper-lockfile on
# cache/validations.json). They come from the build context "contracts": a checkout of agglayer-contracts with the
# dependencies installed and the contracts compiled.
FROM golang:1.27.4 AS polycli-builder
ARG POLYCLI_TAG_OR_COMMIT_SHA="v0.1.123"
WORKDIR /opt/polygon-cli
RUN git clone https://github.com/0xPolygon/polygon-cli.git . \
  && git checkout ${POLYCLI_TAG_OR_COMMIT_SHA} \
  && make build

# Node 24, as the contracts are compiled with: Node 26 breaks the deploy scripts (see the Dockerfile of kurtosis-cdk)
FROM node:24.19.0-bookworm
ARG FOUNDRY_VERSION="v1.4.4"

WORKDIR /opt/agglayer-contracts
COPY --from=contracts . .

COPY --from=polycli-builder /opt/polygon-cli/out/polycli /usr/bin/polycli
WORKDIR /opt
RUN apt-get update \
  && apt-get install --yes --no-install-recommends curl git jq pipx python3-pip \
  && apt-get clean \
  && rm -rf /var/lib/apt/lists/* \
  && pipx ensurepath \
  && pipx install yq \
  && curl --silent --location --proto "=https" https://foundry.paradigm.xyz | bash \
  && /root/.foundry/bin/foundryup --install ${FOUNDRY_VERSION} \
  && cp /root/.foundry/bin/* /usr/local/bin \
  && pip3 install --no-cache-dir --break-system-packages flask flask_wtf gunicorn

USER 1000
