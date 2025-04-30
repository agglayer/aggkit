# EthTxManager

EthTxManager is responsible for managing transactions


## EthTxManager Configuration

| Parameter | Type | Description | Example/Default |
|:---|:---|:---|:---|
| `FrequencyToMonitorTxs` | `duration` | Frequency to monitor pending transactions. | `"1s"` |
| `WaitTxToBeMined` | `duration` | Wait time before retrying mining confirmation. | `"2s"` |
| `GetReceiptMaxTime` | `duration` | Max wait time for getting transaction receipt. | `"250ms"` |
| `GetReceiptWaitInterval` | `duration` | Interval between retries for fetching receipt. | `"1s"` |
| `PrivateKeys` | `array` | List of private key configurations (keystore path + password). | `[ { Path = "/app/keystore/claimsponsor.keystore", Password = "testonly" } ]` |
| `ForcedGas` | `uint64` | Fixed gas value override (0 = no override). | `0` |
| `GasPriceMarginFactor` | `float64` | Gas price multiplier margin. | `1.0` |
| `MaxGasPriceLimit` | `uint64` | Maximum gas price allowed for sending. | `0` |
| `StoragePath` | `string` | Path to EthTxManager's local database. | `"/tmp/aggkit/ethtxmanager-claimsponsor.sqlite"` |
| `ReadPendingL1Txs` | `bool` | Whether to read pending L1 transactions. | `false` |
| `SafeStatusL1NumberOfBlocks` | `uint64` | Number of blocks to consider a transaction safe. | `5` |
| `FinalizedStatusL1NumberOfBlocks` | `uint64` | Number of blocks to consider a transaction finalized. | `10` |

---