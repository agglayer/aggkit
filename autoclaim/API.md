# API

The `autoclaim` component exposes the following public API:

## Constructor

```go
func New(cfg Config, deps ...) (*AutoClaim, error)
```

Instantiated from `cmd.go`. Wires all internal subcomponents and validates configuration.

## Lifecycle

```go
func (a *AutoClaim) Start(ctx context.Context) error
```

Starts the service loop. Blocks until the context is cancelled or an unrecoverable error occurs.

## Config

### Configure  EthTxManager
Internally the component use eth-tx-manager to send the claim transactions it must be configured

Example:
```toml
[EthTxManager]
    FrequencyToMonitorTxs = "1s"
    WaitTxToBeMined = "2s"
    GetReceiptMaxTime = "250ms"
    GetReceiptWaitInterval = "1s"
    PrivateKeys = [
        {Method =  "local", Path = "/app/keystore/aggoracle.keystore", Password = "testonly"},
    ]
    ForcedGas = 0
    GasPriceMarginFactor = 1
    MaxGasPriceLimit = 0
    StoragePath = "{{PathRWData}}/ethtxmanager-aggoracle.sqlite"
    ReadPendingL1Txs = false
    SafeStatusL1NumberOfBlocks = 5
    FinalizedStatusL1NumberOfBlocks = 10
    EstimateGasMaxRetries = 1
        [AggOracle.EVMSender.EthTxManager.Etherman]
            URL = "{{L2URL}}"
            MultiGasProvider = false
            # L1ChainID = 0 indicates it will be set at runtime
            # This field should be populated with L2ChainID
            L1ChainID = 0
            HTTPHeaders = []
```

### Configure starting point

Must be several ways of setting the starting point:

- TimeStamp: The new bridges contained in first  GER after this tstamp is going to be claims and so on.
- L1 block number: The new bridges contained in first GER after this L1 block is going to be claims and so on
- Last claimed GER: The bridges contained in next GER after this is going be claimed

All of them can be configure as `latest` values, so **timestamp** is now, **L1 block number** is `latest`, **last claimed GER** is the latest GER in L1InfoTree

Proposed example of configuration: 

```toml
[StartingPoint]
type = "timestamp"
timestamp = 1713888000
```

### Configure filters

Currently there are only 1 filter that if messages are claimed automatically or not, in the future there must be more things

Proposed example of configuration: 

```toml
[[Filter]]
type = "message"
autoclaim = true
```
