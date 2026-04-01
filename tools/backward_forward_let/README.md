# `backward-forward-let`

`backward-forward-let` diagnoses and recovers Local Exit Tree divergence between the
AggLayer's settled state and the current L2 bridge state.

It also includes staging-only helper commands for certificate injection drills.

## What the tool does

The main command:

- reads the settled AggLayer state,
- reads the current L2 bridge state,
- queries aggsender for settled certificate bridge exits,
- finds the divergence point,
- classifies the recovery case,
- prints the recovery plan,
- activates emergency state when needed,
- executes `BackwardLET` and/or `ForwardLET`,
- verifies the post-step deposit count and LER,
- deactivates emergency state before exit.

## Configuration

The tool reads the same TOML config format as aggkit and requires these sections:

- `Common.L2RPC.URL`
- `BridgeL2Sync.BridgeAddr`
- `AgglayerClient`
- `BackwardForwardLET.BridgeServiceURL`
- `BackwardForwardLET.AggsenderRPCURL`
- `BackwardForwardLET.L2NetworkID`
- `BackwardForwardLET.GERRemoverKey`
- `BackwardForwardLET.EmergencyPauserKey`
- `BackwardForwardLET.EmergencyUnpauserKey`

Example:

```toml
[Common.L2RPC]
URL = "http://localhost:8545"

[BridgeL2Sync]
BridgeAddr = "0x1111111111111111111111111111111111111111"

[AgglayerClient.GRPC]
URL               = "http://localhost:4443"
MinConnectTimeout = "5s"
RequestTimeout    = "300s"
UseTLS            = false

[BackwardForwardLET]
BridgeServiceURL = "http://localhost:8080/bridge/v1"
AggsenderRPCURL  = "http://localhost:5576"
L2NetworkID      = 1

[BackwardForwardLET.GERRemoverKey]
Method   = "local"
Path     = "/path/to/ger-remover.keystore"
Password = "secret"

[BackwardForwardLET.EmergencyPauserKey]
Method   = "local"
Path     = "/path/to/emergency-pauser.keystore"
Password = "secret"

[BackwardForwardLET.EmergencyUnpauserKey]
Method   = "local"
Path     = "/path/to/emergency-unpauser.keystore"
Password = "secret"
```

Role requirements:

- `GERRemoverKey` must be able to call `backwardLET` and `forwardLET`.
- `EmergencyPauserKey` must be able to activate emergency state.
- `EmergencyUnpauserKey` must be able to deactivate emergency state.

## Main recovery command

Diagnose and, after confirmation, execute recovery:

```bash
backward-forward-let --cfg aggkit-config.toml
```

Run non-interactively:

```bash
backward-forward-let --cfg aggkit-config.toml --yes
```

Use a bridge-exit override file when aggsender cannot provide settled certificate exits:

```bash
backward-forward-let --cfg aggkit-config.toml \
  --cert-exits-file certificate_exits_override.json
```

### Output behavior

The command prints one of:

- `NoDivergence`
- a classified recovery case with divergence details
- a missing-certificate report when certificate exits cannot be loaded

Recovery behavior by case:

- Case 1 and Case 3: `ForwardLET` only
- Case 2 and Case 4: `BackwardLET`, then `ForwardLET`, and a second `ForwardLET` when
  extra real L2 bridges must be replayed

## Fallback when aggsender data is unavailable

If the tool reports missing certificate exits, fetch them from the AggLayer admin/debug
endpoint and rerun with `--cert-exits-file`.

Detailed procedure:

- [`RECOVERY_PROCEDURE.md`](./RECOVERY_PROCEDURE.md)

That document covers:

- enabling `debug-mode = true`,
- reaching the AggLayer admin JSON-RPC API,
- using `admin_getCertificate`,
- building the override file,
- handling heights whose cert ID is not auto-resolved.

## Commands

### `backward-forward-let`

Diagnose and recover divergence.

Flags:

- `--cfg`, `-c`: one or more config files
- `--yes`: skip interactive confirmation
- `--cert-exits-file`, `-f`: fallback JSON file with bridge exits keyed by certificate height

### `backward-forward-let send-cert`

Send a certificate JSON to the AggLayer and optionally store it in the aggsender DB.

This is primarily useful for controlled staging drills and test tooling.

Example:

```bash
backward-forward-let send-cert \
  --cfg agglayer-only.toml \
  --cert-file /tmp/cert.json \
  --db-path /path/to/aggsender.sqlite
```

For fallback-mechanism drills where aggsender must not retain the certificate, send to
AggLayer only:

```bash
backward-forward-let send-cert \
  --cfg agglayer-only.toml \
  --cert-file /tmp/cert.json \
  --no-db
```

Flags:

- `--cfg`, `-c`: config file containing at least `AgglayerClient`
- `--cert-json`: certificate JSON string
- `--cert-file`, `-f`: certificate JSON file
- `--db-path`: aggsender SQLite DB path
- `--no-db`: skip aggsender DB storage entirely

Behavior:

- sends the certificate to the AggLayer,
- stores it in aggsender DB as the last sent certificate unless `--no-db` is set,
- derives `FromBlock` from the previous certificate when possible so aggsender retry logic remains coherent.

### `backward-forward-let craft-cert`

Build a signed malicious certificate JSON for staging drills.

This command is intentionally gated by `--staging-only`.

Example:

```bash
backward-forward-let craft-cert \
  --cfg aggkit-config.toml \
  --signer-key-path /path/to/sequencer.keystore \
  --signer-key-password 'secret' \
  --staging-only \
  --num-fake-exits 1 \
  --out /tmp/malicious-cert.json
```

If aggkit/aggsender is stopped and aggsender RPC is unavailable, add `--db-path` so the
command can reconstruct prior settled bridge exits from the aggsender SQLite DB:

```bash
backward-forward-let craft-cert \
  --cfg aggkit-config.toml \
  --signer-key-path /path/to/sequencer.keystore \
  --signer-key-password 'secret' \
  --db-path /path/to/aggsender.sqlite \
  --staging-only \
  --num-fake-exits 2 \
  --out /tmp/malicious-cert.json
```

Flags:

- `--cfg`, `-c`: config file with normal tool connectivity settings
- `--signer-key-path`: keystore used to sign the crafted certificate
- `--signer-key-password`: password for the signer keystore
- `--out`: write crafted JSON to a file instead of stdout
- `--db-path`: optional aggsender SQLite DB path when aggsender RPC is unavailable
- `--num-fake-exits`: number of fake exits to include
- `--starting-exit-index`: start index used to derive unique destination addresses
- `--nonce`: optional deterministic nonce used in fake destination derivation
- `--origin-network`: fake exit origin network
- `--origin-token-address`: fake exit origin token address
- `--destination-network`: fake exit destination network
- `--amount`: decimal amount for each fake exit
- `--staging-only`: required acknowledgement

Behavior:

- reads the current settled state from AggLayer,
- reconstructs the existing leaf sequence from aggsender RPC or aggsender DB,
- builds one or more fake `BridgeExit`s,
- computes the resulting `NewLocalExitRoot`,
- signs the crafted certificate,
- writes JSON that can be consumed by `send-cert`.

## Staging drill flow

To simulate divergence on a staging network:

1. Stop aggkit/aggsender so no genuine certificate races with the drill.
2. Craft a malicious certificate with `craft-cert`.
3. Submit it with `send-cert`.
   Use `--no-db` if you specifically want to test the fallback path where aggsender cannot
   provide certificate bridge exits and operators must use the AggLayer admin/debug endpoint.
4. Restart aggkit/aggsender and wait for the certificate to settle.
5. Optionally create extra real L2 bridges if you want a Case 2 or Case 4 drill.
6. Run `backward-forward-let --cfg ...` to diagnose and recover.

Typical case mapping:

- Case 1: one malicious cert, no extra L2 bridges
- Case 2: one malicious cert, then extra real L2 bridges
- Case 3: two malicious certs, no extra L2 bridges
- Case 4: two malicious certs, then extra real L2 bridges

## Safety notes

- `craft-cert` and `send-cert` are for staging drills and controlled test environments.
- Do not use the debug commands against a production network.
- The recovery command itself is intended for real incidents, but only with the correct
  signer roles and a verified operator workflow.
