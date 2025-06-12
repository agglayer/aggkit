# Etherman
Etherman handles the communication with the network.

## Etherman Configuration

| Parameter          | Type     | Description                                                                                       | Example/Default |
|:-------------------|:---------|:--------------------------------------------------------------------------------------------------|:----------------|
| `URL`              | `string` | JSON-RPC URL for the network.                                                                     |                 |
| `MultiGasProvider` | `bool`   | Use multiple gas providers if true.                                                               | `false`         |
| `L1ChainID`        | `uint64` | The Chain ID of the network to which transactions will be sent.<br><br>**Note:** This can be either the L1 or L2 Chain ID. |                 |
| `HTTPHeaders`      | `array`  | Custom HTTP headers to add to RPC calls.                                                          | `[]`            |

---

**Note:** If the `L1ChainID` field is set to `0`, Etherman will automatically determine and populate the correct Chain ID at runtime, provided that a valid JSON-RPC URL is supplied.
