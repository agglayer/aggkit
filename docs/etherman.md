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

## JSON-RPC batch sizing

When `BatchBlockHeaderRetrieval` is enabled (see [`RPCClientConfig`](./common_config.md#rpcclientconfig)), Etherman
fetches block headers with JSON-RPC batch requests of at most `BatchRequestMaxSize` requests (default `1000`; `0`
means the default and negative values are rejected at startup).

Many providers enforce a lower per-batch limit. Etherman adapts to it at runtime instead of failing:

- A batch is considered too large only when the provider answers with a recognised batch-limit error: a message such
  as `too many batch requests, max is N`, `batch limit N exceeded` or `batch of more than N requests`, other
  recognised batch-limit wording, or HTTP `413`. Any other error (including HTTP `429`) does not change the batch
  size and follows the normal retry handling.
- When the message carries the provider limit `N` (and it is smaller than the rejected batch), the batch size
  shrinks directly to `N`. Otherwise it is halved (minimum `1`).
- Only the block numbers of the rejected batches are requested again, with the new size; headers already retrieved
  are kept.
- The reduced size is kept on the client for the lifetime of the process and never grows back. Set
  `BatchRequestMaxSize` to the provider limit to avoid the first rejection.
- Each reduction is logged at Warn: `etherman: provider rejected a JSON-RPC batch of <n> requests, reducing batch
  size <old> -> <new>: <error>`.

## Downloader resilience to RPC provider behaviour

### Multidownloader step back-off

If a step of the multidownloader fails (`Start` loop), it is retried with an exponential back-off: 100 ms initial
delay, factor 2, capped at 10 s, with +-20 % jitter. The back-off resets after a successful step. A repeated
error is logged at Warn the first time it occurs and then summarised once a minute (with the number of repetitions)
instead of once per attempt. The log text to grep for is:

```text
EVMMultidownloader(<name>).Start: error running multidownloader step
```

A confirmed `eth_getLogs` omission in the multidownloader (a block whose logs bloom is non-empty but the range query
returned no logs) is retried with this same back-off.

### eth_getLogs omission handling in the legacy syncers

The `sync` package downloader (used by the bridge, claim and similar syncers) checks that a range query did not
silently drop the logs of a block (some providers omit results). A block above the last finalized block whose header
bloom says it has logs but whose range result has none is re-queried by block hash:

- If the by-hash query returns logs, the omission is confirmed and those logs are spliced into the range result, in
  block order, before topic filtering, so they go through the same path as the logs of the range query. No range
  retry is needed.
- At most 8 omitted blocks per range are recovered this way. Above that, or when the by-hash query cannot be
  completed (two attempts per block), the whole range is downloaded again, as before. A bloom false positive (the
  by-hash query also returns nothing) is not treated as an omission.

Log lines (the first three are the new or changed ones):

- `logs completeness check: eth_getLogs omitted logs for <n> block(s) in range [<from>,<to>]; spliced <m> log(s)
  recovered by block-hash query` (Warn).
- `eth_getLogs completeness check failed for range [<from>,<to>] (attempt <n>, <k> block(s) confirmed omitted,
  splice cap 8), retrying range download` (Error for the first 5 attempts and then every 100th, Debug otherwise).
- `logs completeness check: could not arbitrate suspicious block <n> (hash <h>): all 2 re-query attempts failed;
  conservatively retrying the range` (Warn).

Prometheus metrics (exported when Prometheus is enabled; label `syncer` is the syncer identifier):

| **Metric Name** | **Type** | **Description** |
| --- | --- | --- |
| `sync_logs_omission_confirmed_total{syncer}` | Counter | `eth_getLogs` omissions confirmed by block-hash arbitration. Counted per check: a block re-confirmed after a range retry counts again |
| `sync_logs_omission_range_retries_total{syncer}` | Counter | Whole-range retries triggered by the completeness check (cap exceeded or arbitration unverifiable) |
