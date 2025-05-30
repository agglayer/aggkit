#!/bin/bash
source $(dirname $0)/scripts/env.sh

FORK=$1
if [ -z $FORK ]; then
    echo "Missing FORK: [valid values: 'fork12']"
    exit 1
fi

DATA_AVAILABILITY_MODE=$2
if [ -z $DATA_AVAILABILITY_MODE ]; then
    echo "Missing DATA_AVAILABILITY_MODE: ['rollup', 'cdk-validium', 'pessimistic']"
    exit 1
fi

BASE_FOLDER=$(dirname $0)
docker images -q aggkit:latest > /dev/null
if [ $? -ne 0 ] ; then
    echo "Building aggkit:latest docker image"
    pushd $BASE_FOLDER/..
    make build-docker
    popd
else
    echo "docker image aggkit:latest already exists"
fi

kurtosis clean --all
echo "Override aggkit config file"
cp $BASE_FOLDER/config/kurtosis-cdk-node-config.toml.template $KURTOSIS_FOLDER/templates/trusted-node/cdk-node-config.toml
KURTOSIS_CONFIG_FILE="combinations/$FORK-$DATA_AVAILABILITY_MODE.yml"
TEMP_CONFIG_FILE=$(mktemp --suffix ".yml")
echo "rendering $KURTOSIS_CONFIG_FILE to temp file $TEMP_CONFIG_FILE"
go run ../scripts/run_template.go $KURTOSIS_CONFIG_FILE > $TEMP_CONFIG_FILE
kurtosis run --enclave $KURTOSIS_ENCLAVE --args-file "$TEMP_CONFIG_FILE" --image-download always $KURTOSIS_FOLDER
rm $TEMP_CONFIG_FILE
