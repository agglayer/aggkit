# AggSender component

## Introduction

`AggSender` is in charge to build and pack the information required to prove a target chain’s bridge state into a certificate. This certificate provides the inputs needed to build a pessimistic proof.

## Component Diagram
(TBD)

## Flow

### Starting the AggSender

Aggsender gets the epoch configuration from the Agglayer. 
It checks the last certificate in DB (if exists) against the Agglayer, to be sure that both are on the same page:
    - If the DB is empty then get, as starting point, the last certificate agglayer has.
    - If it is a fresh start, and there are no certificates before this, it will set its starting block to 1 and start polling bridges and claims from the syncer from that block.

```mermaid
sequenceDiagram
    participant agglayer
    participant aggsender

    aggsender->>agglayer: Read epoch configuration
    aggsender->>agglayer: Read latest known certificate
    aggsender-->>aggsender: Wait for an epoch
    aggsender->>agglayer: Send certificate
```

### Build a certificate

Aggsender will wait until the epoch event is triggered and ask the `L2BridgeSyncer` if there are new bridge to be sent to agglayer. Once we reach the moment in epoch when we need to send a certificate, the aggsender will poll all the bridges and claims from the bridge syncer, based on the last sent L2 block to the agglayer, until the block that the syncer has.

It is important to mention that no certificate will be sent to the agglayer if the syncer has no bridges, since bridges change the Local Exit Root (LER).

If we have bridges, certificate will be built, signed, and sent to the agglayer using the provided agglayer RPC URL.

Currently, agglayer only supports one certificate per L1 epoch, per network, so we can not send more than one certificate. After the certificate is sent, we wait until the next epoch, either to resend it if its status is InError, or to build a new one if its status Settled. Also, we have no limit yet in how many bridges and claims can be sent in a single certificate. This might be something to test and check, because certificates carry a lot of data through RPC, so we might hit a limit at some point.

InError status can mean a number of things. It can be an error that happened on the agglayer. It can be an error in the data aggsender sent, or the certificate was sent in between two epochs, which agglayer considers invalid. Either way, the given certificate needs to be re-sent in the next epoch, with all the previously sent bridges and claims, plus the new ones that happened after them, that the syncer saw and saved. 

It is important to mention that, in the case of resending the certificate, the certificate height must be reused. If we are sending a new certificate, its height must be incremented based on the previously sent certificate.

Suppose the previously sent certificate was not marked as InError, or Settled on the agglayer. In that case, we can not send/resend the certificate, even though a new epoch event is handled.

(TBD sequence diagram)

## Certificate data

The certificate is the data submitted to `Agglayer`. Must be signed to be accepted by `Agglayer`. `Agglayer` responds with a `certificateID` (hash)

| Field Name               | Description                                                                 |
|--------------------------|-----------------------------------------------------------------------------|
| `network_id`               | This is the id of the rollup (>0)                                          |
| `height`                   | Order of certificates. First one is 0                                      |
| `prev_local_exit_root`     | The first one must be the one in SMC (currently is a 0x000…00)             |
| `new_local_exit_root`      | It’s the root after bridge_exits                                           |
| `bridge_exits`             | These are the leaves of the LER tree included in this certificate. (bridgeAssert calls) |
| `imported_bridge_exits` (claims) | These are the claims done in this network                                |

## Configuration

| Name                          | Type               | Description                                                                                                   |
|-------------------------------|--------------------|---------------------------------------------------------------------------------------------------------------|
| StoragePath                   | string             | Path where to store aggsender DB                                                                              |
| AggLayerURL                   | string             | URL to Agglayer                                                                                               |
| BlockGetInterval              |                    | Deprecated (CDK-616)                                                                                          |
| CheckSettledInterval          |                    | Deprecated (CDK-616)                                                                                          |
| AggsenderPrivateKey           | KeystoreFileConfig | Private key used to sign the certificate on the aggsender before sending it to the agglayer. Must be configured the same as on agglayer. |
| URLRPCL2                      | string             | L2 RPC                                                                                                       |
| BlockFinality                 | string             | Block type to calculate epochs on L1.                                                                         |
| EpochNotificationPercentage   | uint               | 0 -> at beginning of epoch  <br> 100 -> at end of the epoch  <br> (default: 50)                                |
| SaveCertificatesToFilesPath   | string             | This is a default option to store the certificate as a file. <br> Files: certificate_<height>-<tstamp>.json    |
| MaxRetriesStoreCertificate    | int                | Number of retries if aggsender fails to store certificates on DB                                              |
| DelayBeetweenRetries          | Duration           | Initial status check delay <br> Store certificate on DB delay                                                 |
| KeepCertificatesHistory       | bool               | Instead of deleting them, discarded certificates are moved to certificate_info_history table                   |

## Use Cases

This paragraph explains different use cases with outcomes.

- No bridges from L2 -> L1 means no certificate will be built.
- Having bridges without claims, means a certificate will be built and sent.
- Having bridges and claims, means a certificate will be built and sent.
- If the previous certificate we sent is `InError`, we need to resend that certificate with all the previous sent data, plus new bridges and claims we saw after that.
- If the previously sent certificate is not `InError` or `Settled`, no new certificate will be sent/resent. The `AggSender` waits for one of these two statuses on the `Agglayer`.
