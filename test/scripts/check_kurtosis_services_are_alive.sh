#!/usr/bin/env bash

function help() {
    echo "Usage: $0 <timeout> <kurtosis enclave> <service1> <service2> ..."
    echo "This script checks if the specified Kurtosis services are alive within the given timeout."
}

TIMEOUT=$1
shift
if [ -z "$TIMEOUT" ]; then
    help
    exit 1
fi
KURTOSIS_ENCLAVE=$1
shift
if [ -z "$KURTOSIS_ENCLAVE" ]; then
    help
    exit 1
fi
if [ -z "$*" ]; then
    help
    exit 1
fi
START_TIME=$(date +%s)
END_TIME=$((START_TIME + TIMEOUT))
while true; do
    DEAD=""
    for service in  $*; do
        SRV_STATUS=$(kurtosis service inspect "$KURTOSIS_ENCLAVE" "$service" | grep Status | cut -f 2 -d ':' | tr -d ' ')
        if [ "$SRV_STATUS" != "RUNNING" ]; then
            DEAD="$DEAD $service"
        fi
    done
    if [ -z "$DEAD" ]; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] ✅ All services are alive in Kurtosis enclave $KURTOSIS_ENCLAVE"
        exit 0
    fi

    CURRENT_TIME=$(date +%s)
    if ((CURRENT_TIME > END_TIME)); then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] ❌ Exiting... next services [ $DEAD ] are not alive in Kurtosis enclave $KURTOSIS_ENCLAVE"        exit 1
    fi
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Waiting to services [ $DEAD ] to be alive in Kurtosis enclave $KURTOSIS_ENCLAVE"        exit 1

    sleep 5
done