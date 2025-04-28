# Claimsponsor Component

ClaimSponsor is a service that automatically submits asset or message claims to the Layer 2 (L2) bridge contract on behalf of users.

## ClaimSponsor Configuration 

| Parameter | Type | Description | Example/Default |
|:---|:---|:---|:---|
| `DBPath` | `string` | Path to the local SQLite database for storing claim data. | `"/tmp/aggkit/claimsponsor.sqlite"` |
| `Enabled` | `bool` | Whether the ClaimSponsor service is enabled. | `false` |
| `SenderAddr` | `string` | Address that submits claims to the bridge. |  |
| `BridgeAddrL2` | `string` | Address of the bridge contract on L2. |  |
| `MaxGas` | `uint64` | Maximum gas limit for a claim transaction. | `200000` |
| `RetryAfterErrorPeriod` | `duration` | Time to wait after an error before retrying. | `"1s"` |
| `MaxRetryAttemptsAfterError` | `int` | Maximum retry attempts after errors (-1 for infinite retries). | `-1` |
| `WaitTxToBeMinedPeriod` | `duration` | Polling interval to check if a transaction has been mined. | `"3s"` |
| `WaitOnEmptyQueue` | `duration` | Wait time before checking the queue again if empty. | `"3s"` |
| `GasOffset` | `uint64` | Additional gas to add to the estimated amount. | `0` |

---


For detailed configuration of the Ethereum Transaction Manager (EthTxManager), refer to [EthTxManager Configuration](./ethtxmanager.md).

For detailed configuration of Etherman, refer to [Etherman Configuration](./etherman.md).