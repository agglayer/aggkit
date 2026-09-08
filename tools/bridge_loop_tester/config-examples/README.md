# Example configurations

`example.toml` is a **documentation fixture, not a deployable config**: every RPC URL, bridge
address and signer keystore path in it is an obvious placeholder. It targets a hypothetical
3-network environment shaped like `test/e2e/envs/anvil-2chains` (network `0` is L1, networks `1`
and `2` are L2s, one aggkit-proxy fronts `/bridge/v1` + `/tracker/v1` for all three) — see
`tools/bridge_loop_tester/DESIGN.md` for the wire contract this shape assumes.

It defines two loops, both closed rings covering all three bridge directions
(L1→L2, L2→L2, L2→L1) and both mixing `"auto"` and `"manual"` claim hops:

- **`eth-ring`** — moves ETH around `0 → 1 → 2 → 0`.
- **`erc20-ring`** — moves a test ERC20 (deployed on network `1` via `deploy-token`) around
  `1 → 2 → 0 → 1`.

It passes `(*Config).Validate()`'s static checks (schema + ring closure) as committed — see the
main README's `validate` section for what else that command checks once pointed at real endpoints.

## Fields you must change before running it against a real environment

| Field | Why |
|---|---|
| `Global.ProxyURL` | Point it at your aggkit-proxy's REST base URL. |
| `Global.MetricsAddr` | Change or clear the port if `9090` collides with something else, or leave empty to disable metrics. |
| `Networks[].RPCURL` | Each network's real JSON-RPC endpoint. |
| `Networks[].BridgeAddr` | Each network's real bridge contract address — `validate` cross-checks this against the bridge's own `networkID()` and against what the proxy publishes, so a wrong address is caught quickly rather than causing every readiness gate to silently stall. |
| `Networks[].Signer` | Real signer configuration (`Method = "local"` with a real keystore `Path`/`Password`, or an AWS/GCP KMS config — see `github.com/agglayer/go_signer/signer/types`). **The example's keystore paths and passwords are placeholders and will not decode any real key.** |
| `Networks[].MinNativeReserve` | Set this deliberately per network rather than leaving the example's `1 ETH` — see the main README's "Gas drain, funding and `MinNativeReserve`" section for how to size it for a multi-day run. |
| `Loops[].TokenOriginNetwork` (erc20 loop only) | Must match the network you actually deploy the test ERC20 on with `deploy-token --network N --loop erc20-ring`. |
| `Loops[].Amount` | Size it to what you actually want to soak-test moving, and to what `MinNativeReserve`/available balances can sustain per cycle. |

## Adapting it to a different topology

- **Fewer or more networks**: the ring-closure rule only requires each `Loop.Hops` to chain and
  close — it does not require every configured `[[Networks]]` entry to participate in every loop, or
  every loop to have the same number of hops. A 2-network ring (`0 → 1 → 0`) is valid; so is a
  5-network one.
- **Claim-mode assignment**: assign `Claim = "auto"` to a hop whose *destination* network is one
  where you expect (or are testing) an autoclaim service to be active for that route, and
  `Claim = "manual"` to a hop whose destination has no such policy. Mixing both in one loop, as the
  example does, is what lets a single loop exercise both halves of the claim-mode contract described
  in the main README.
- **Multiple ERC20 loops**: give each its own `Name` and `TokenOriginNetwork`; `deploy-token`'s
  `--loop` flag records each token address under its own loop name in the state file, so they never
  collide.

## What is deliberately not here

No bali-specific values, no real RPC URLs or hostnames, no real contract addresses, and no real
keys — see the main README's "Known limitations" section and the plan's "out of scope" note: the
dry-dock/bali environment work is a separate, later effort, and this directory does not anticipate
it.
