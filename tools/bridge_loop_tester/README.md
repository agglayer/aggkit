# Bridge Loop Tester

A long-running soak-test tool that continuously moves value around circular bridge routes to test all three bridge directions (L1→L2, L2→L1, L2→L2) using ETH and a deployed ERC20 token.

## Overview

The bridge loop tester continuously executes circular routes where:
- Value starts at a source address
- Moves through one or more bridge hops
- Returns to the origin address after each complete cycle

This design allows for days of testing without requiring refunding, as only gas is consumed and value circulates back to its starting point.

## Features

- **Circular route testing**: L1→L2, L2→L1, and L2→L2 bridge directions
- **Multi-hop routes**: Chain multiple bridge operations in sequence
- **ETH and ERC20 support**: Test both native and token transfers
- **Flexible claim modes**: Configure per-hop claims as automatic or manual
- **REST API integration**: All observations via aggkit proxy REST API (`/bridge/v1` + `/tracker/v1`)
- **Direct RPC access**: Monitor network state through JSON-RPC endpoints

## Configuration

Configuration is specified via TOML files and passed with the `--cfg` / `-c` flag (repeatable).

Each hop in the configuration specifies:
- Source and destination networks
- Token to transfer (ETH or deployed ERC20)
- Amount to move
- Claim strategy: `"auto"` (autoclaim service) or `"manual"` (tool performs claim)

Example configuration files are in the `config-examples/` directory.

## Commands

### `run`
Execute the continuous bridge loop test.

```bash
bridge-loop-tester run --cfg config.toml
```

### `validate`
Validate the configuration without running the loop.

```bash
bridge-loop-tester validate --cfg config.toml
```

### `deploy-token`
Deploy an ERC20 token on the specified network.

```bash
bridge-loop-tester deploy-token --cfg config.toml
```

### `claim`
Manually claim pending bridge exits.

```bash
bridge-loop-tester claim --cfg config.toml
```

### `status`
Display the current status of the loop and pending claims.

```bash
bridge-loop-tester status --cfg config.toml
```

## Requirements

- Access to aggkit proxy REST API endpoints
- JSON-RPC endpoints for monitored networks
- Sufficient gas balance on source addresses
- Deployed ERC20 contracts (if using token transfers)

## Integration

Observations are made exclusively through:
- aggkit proxy REST API (`/bridge/v1` and `/tracker/v1` endpoints)
- JSON-RPC endpoints of each network

No internal aggkit package storage or database is accessed directly.
