#!/bin/bash
source $(dirname $0)/scripts/env.sh

FORK=$1
if [ -z $FORK ]; then
    echo "Missing FORK parameter: [valid values: 'fork12']" >&2
    exit 1
fi

DATA_AVAILABILITY_MODE=$2
if [ -z $DATA_AVAILABILITY_MODE ]; then
    echo "Missing DATA_AVAILABILITY_MODE parameter: ['pessimistic', 'op-succint']" >&2
    exit 1
fi

BASE_FOLDER=$(dirname $0)
if [ "$(docker images -q aggkit:local | wc -l)" -eq 0 ]; then
    echo "Building aggkit:local docker image" >&2
    pushd $BASE_FOLDER/..
    make build-docker
    popd
else
    echo "docker image aggkit:local already exists" >&2
fi

kurtosis clean --all
echo "Override aggkit config file" >&2
cp $BASE_FOLDER/config/kurtosis-cdk-node-config.toml.template $KURTOSIS_FOLDER/templates/trusted-node/cdk-node-config.toml
KURTOSIS_CONFIG_FILE="combinations/$FORK-$DATA_AVAILABILITY_MODE.yml"
TEMP_CONFIG_FILE=$(mktemp "aggkit-kurtosis.yml-XXXXX")
echo "rendering $KURTOSIS_CONFIG_FILE to temp file $TEMP_CONFIG_FILE" >&2
go run ../scripts/run_template.go $KURTOSIS_CONFIG_FILE >$TEMP_CONFIG_FILE
kurtosis run --enclave $KURTOSIS_ENCLAVE --args-file "$TEMP_CONFIG_FILE" --image-download always $KURTOSIS_FOLDER

rm $TEMP_CONFIG_FILE

echo "Kurtosis environment $KURTOSIS_ENCLAVE is up and running" >&2
