# Circle USDC (USDC.e, usdc-lxly) contracts for the e2e

Creation bytecode (`*.bin`: plain hex, no `0x`, no trailing newline) of the contracts behind Circle's
USDC.e on Polygon zkEVM and of the zkEVM-sunset recovery package (agglayer/issues#298), so the e2e deploys
them with `cast send --create` and needs no compiler or download at run time.

**The Solidity sources are not vendored here, for licensing reasons** (LGPL-3.0, AGPL-3.0 and MIT code from several
upstreams): this directory only holds the `.bin` files, `move.sh.tpl`, `build.sh` and this README. The table below
gives the origin of each `.bin`; `build.sh` fetches the sources from those repositories at the pinned commits into a
temp dir, compiles them there and checks the result against the committed `.bin` (see "Regenerating").

## Contracts

| `.bin` | Upstream (commit, path) | Licence | Compiler | sha256 of the `.bin` |
| --- | --- | --- | --- | --- |
| `FiatTokenV2_1` | [circlefin/stablecoin-evm](https://github.com/circlefin/stablecoin-evm) `b5aba6ce` (tag v2.1.0) `contracts/v2/FiatTokenV2_1.sol`, with OpenZeppelin Contracts `de99bccb` (v3.1.0) | MIT | solc 0.6.12, optimizer 200, istanbul | `b625d9388f42578d030fe81978f028c476aa45d8d6b6e7ebea48b08738574775` |
| `FiatTokenProxy` | same, `contracts/v1/FiatTokenProxy.sol` | MIT | same | `e2349a36719c6f6728376dc52322afd7233f3d30aa26621e4975bc327a7f85c6` |
| `L1Escrow` | [BuildOnPolygon/usdc-lxly](https://github.com/BuildOnPolygon/usdc-lxly) `d7cea0d4` (main) `src/L1Escrow.sol` | MIT (SPDX; the repository has no LICENSE file) + AGPL-3.0 (zkevm-contracts interfaces) | solc 0.8.17, optimizer 200, london | `3683e326bcddfd82a5c5e3dd3a77ae4a0327decdc7dca241966d10a1125699c8` |
| `L1EscrowProxy` | same, `src/L1EscrowProxy.sol` | MIT | same | `876fa92d1f59c226d8989a29db133b4463b5d01e7d32433b0a45aed690982708` |
| `ZkMinterBurner` | same, `src/ZkMinterBurner.sol` | as `L1Escrow` | same | `6ca58ecb4aa6b0e2bc8c9d825068fc3b3ee6e8754840b235942f44466d3456b1` |
| `ZkMinterBurnerProxy` | same, `src/ZkMinterBurnerProxy.sol` | MIT | same | `3076054228890ba8a14450242bfafb794056343639ab74150ec0ee39adbe8bd3` |
| `NativeConverter` | same, `src/NativeConverter.sol` | as `L1Escrow` | same | `3c361a1007c5b9271b7fa480c8d4b3704623bcdc44b34a33182c64ceaa0563f9` |
| `NativeConverterProxy` | same, `src/NativeConverterProxy.sol` | MIT | same | `572165235e2408740c8f86e9bd769fea167de6f5bdea9a1225ead01c115c9b0e` |
| `GnosisSafeL2` | [safe-global/safe-smart-account](https://github.com/safe-global/safe-smart-account) `186a21a7` (tag v1.3.0) `contracts/GnosisSafeL2.sol` | LGPL-3.0-only | solc 0.7.6, optimizer disabled, istanbul | `c7d54ce3fb8d63cbdd46173ed5eb95db147e6f98bdc5e62d267b44e2e3965712` |
| `GnosisSafeProxyFactory` | same, `contracts/proxies/GnosisSafeProxyFactory.sol` | LGPL-3.0-only | same | `741d5daa0701dadcca475b4236a47b743bc75bbe6ee81c8630e47d3f47769c85` |
| `MultiSendCallOnly` | same, `contracts/libraries/MultiSendCallOnly.sol` | LGPL-3.0-only | same | `2adccc150d1b624b13d2b3a28f6763778bd5930aa398b35a16c748922e22e589` |
| `ZkMinterBurnerPayout` | recovery package (agglayer/protocol-team-kanban#819, v2), `contracts/src/ZkMinterBurnerPayout.sol`; **no public repository** (source sha256 `ed2e8d37…f40b`, pinned in `build.sh`) | AGPL-3.0 | solc 0.8.28, optimizer 200, shanghai, no metadata | `bc2022a8ee4e83bee7989cac1bc0dce6e6d2a6107590e81bc7cb6dc2039d483b` |
| `UsdcRecoveryBurn` | recovery package, `contracts/src/UsdcRecoveryBurn.sol`; **no public repository** (source sha256 `2918cd7f…d17a`, pinned in `build.sh`) | AGPL-3.0 | same | `2d118dc865d25f5f265c0b66e9ecfc1bac15245507a5b76fda23c350e15a5cc3` |

usdc-lxly submodules: OpenZeppelin Contracts `e50c24f5` and Contracts Upgradeable `bc95521e` (both v4.9.2),
0xPolygonHermez/zkevm-contracts `b7373620` (`build.sh` checks that usdc-lxly pins them). The `.bin` files are
compiled from the upstream source trees as `build.sh` lays them out. Every runtime is under the 24,576-byte limit (largest: `GnosisSafeL2`, 23,800)
and every init code under 49,152 bytes (largest: `GnosisSafeL2`, 23,841).

Versions and settings are the ones of the zkEVM mainnet deployment, not the upstream defaults: zkEVM USDC.e
runs `FiatTokenV2_1` (it has no `initializeV2_2`), built from stablecoin-evm v2.1.0 with optimizer 200 (the
upstream truffle config says 10,000,000 runs); the zkEVM Safe `0x2be7…9c9f` is a `GnosisSafeL2` proxy. See
"Mainnet comparison". `FiatTokenV2_2` is not used: it also needs the external `SignatureChecker` library
linked in.

## Deploy recipe

`bin X` is `0x$(cat X.bin)`; `send` is `cast send --rpc-url "${RPC_URL}" --json <sender>`, and the new address
is `.contractAddress` of the receipt. `Z=0x0000000000000000000000000000000000000000`. Order as run in the
rehearsal (L1 and L2 on one anvil, with stub bridges):

```bash
# --- L2: Safe v1.3.0 (e.g. 2 of 3) ---
SINGLETON=$(send --create "$(bin GnosisSafeL2)")
FACTORY=$(send --create "$(bin GnosisSafeProxyFactory)")
MULTISEND=$(send --create "$(bin MultiSendCallOnly)")
SETUP=$(cast calldata 'setup(address[],uint256,address,bytes,address,address,uint256,address)' \
    "[${OWNER1},${OWNER2},${OWNER3}]" 2 "$Z" 0x "$Z" "$Z" 0 "$Z")
send "${FACTORY}" 'createProxy(address,bytes)' "${SINGLETON}" "${SETUP}"
# SAFE = first word of the data of the factory's ProxyCreation(address proxy, address singleton) log:
#   jq -r --arg f "<factory lowercase>" '.logs[] | select((.address|ascii_downcase)==$f) | "0x" + .data[26:66]'

# --- USDC.e: FiatTokenV2_1 behind FiatTokenProxy (zos AdminUpgradeabilityProxy) ---
USDCE_IMPL=$(send --create "$(bin FiatTokenV2_1)")
USDCE=$(send --create "$(bin FiatTokenProxy)$(cast abi-encode 'c(address)' "${USDCE_IMPL}" | cut -c3-)")
send "${USDCE}" 'changeAdmin(address)' "${SAFE}"   # proxy admin = deployer until here
send "${USDCE}" 'initialize(string,string,string,uint8,address,address,address,address)' \
    "USD Coin" USDC USD 6 "${DEPLOYER}" "${SAFE}" "${SAFE}" "${DEPLOYER}"   # masterMinter, pauser, blacklister, owner
send "${USDCE}" 'initializeV2(string)' "USD Coin"
send "${USDCE}" 'initializeV2_1(address)' "${SAFE}"                       # lostAndFound

# --- usdc-lxly (as scripts/DeployInit.s.sol): both proxies first, then initialize ---
L1E_IMPL=$(send --create "$(bin L1Escrow)")                                   # on L1
L1E=$(send --create "$(bin L1EscrowProxy)$(cast abi-encode 'c(address,bytes)' "${L1E_IMPL}" 0x | cut -c3-)")
ZMB_IMPL=$(send --create "$(bin ZkMinterBurner)")                             # on L2
ZMB=$(send --create "$(bin ZkMinterBurnerProxy)$(cast abi-encode 'c(address,bytes)' "${ZMB_IMPL}" 0x | cut -c3-)")
NC_IMPL=$(send --create "$(bin NativeConverter)")                             # on L2
NC=$(send --create "$(bin NativeConverterProxy)$(cast abi-encode 'c(address,bytes)' "${NC_IMPL}" 0x | cut -c3-)")
# initialize(owner, admin, bridge, networkId, peer, token[, bridgeWrappedUSDC]); sender = the proxy deployer
send "${L1E}" 'initialize(address,address,address,uint32,address,address)' "${L1_OWNER}" "${L1_ADMIN}" "${L1_BRIDGE}" "${L2_NETWORK_ID}" "${ZMB}" "${L1_USDC}"
send "${ZMB}" 'initialize(address,address,address,uint32,address,address)' "${SAFE}" "${SAFE}" "${L2_BRIDGE}" 0 "${L1E}" "${USDCE}"
send "${NC}" 'initialize(address,address,address,uint32,address,address,address)' "${SAFE}" "${SAFE}" "${L2_BRIDGE}" 0 "${L1E}" "${USDCE}" "${BW_USDC}"

# --- USDC.e roles, as on mainnet ---
send "${USDCE}" 'configureMinter(address,uint256)' "${ZMB}" 1000000000000000000
send "${USDCE}" 'configureMinter(address,uint256)' "${NC}" 1000000000000000000
send "${USDCE}" 'updateMasterMinter(address)' "${SAFE}"
send "${USDCE}" 'transferOwnership(address)' "${SAFE}"
```

Notes:

- **FiatTokenProxy** sets its admin to `msg.sender` in the constructor, and the admin cannot call the
  implementation through the proxy (`Cannot call fallback function from the proxy admin`). Hence
  `changeAdmin(SAFE)` first, then the deployer (no longer admin) initializes. Admin functions:
  `changeAdmin(address)`, `upgradeTo(address)`, `upgradeToAndCall(address,bytes)` (all `ifAdmin`).
  The initializers must run in this order: `initialize` → `initializeV2` → `initializeV2_1`.
- **usdc-lxly proxies** are plain `ERC1967Proxy`s (OpenZeppelin 4.9.2) whose constructor sets the EIP-1967
  admin to the deployer. `initialize` is `onlyProxy onlyAdmin initializer`, so the deployer must call it; it
  then sets the owner (`OwnableUpgradeable`, storage slot 51 = `0x33`, read by `ZkMinterBurnerPayout`) and
  moves the admin (`_changeAdmin`). Upgrades (`upgradeTo`, `upgradeToAndCall`) are `onlyAdmin`, `pause` is
  `onlyOwner`: the Safe must be both, as on mainnet.
- **Deposit** (L1 → L2): `L1USDC.approve(L1E, amount)`, then `L1E.bridgeToken(address,uint256,bool)(recipient,
  amount, forceUpdateGER)`. It pulls the L1 USDC into the escrow and calls `bridgeMessage(L2_NETWORK_ID, ZMB,
  force, abi.encode(recipient, amount))`; `claimMessage` on L2 calls `ZMB.onMessageReceived`, which mints.
  This is how `L1Escrow` gets its USDC.
- **ZkMinterBurner.onMessageReceived** requires `msg.sender == bridge`, `originAddress == l1Escrow`,
  `originNetwork == l1NetworkId` (0) and not paused; it calls `USDC.e.mint`, so ZMB must be a minter with
  allowance.
- **L1Escrow.onMessageReceived** (withdrawal / payout claim on L1) requires `msg.sender == bridge`
  (`NOT_BRIDGE`), `originAddress == zkMinterBurner` (`NOT_MINTER_BURNER`), `originNetwork == zkNetworkId`
  (`NOT_ZK_CHAIN`), not paused, and receiver and amount non-zero; it then transfers `amount` L1 USDC to the
  receiver of `abi.decode(data, (address, uint256))`.
- `UsdcRecoveryBurn` writes the FiatToken `balances` mapping at slot 9 and `totalSupply_` at slot 11: true for
  this `FiatTokenV2_1` build (checked on anvil) and for zkEVM USDC.e (checked with `cast storage`).

## Bytecode substitution (`ZkMinterBurnerPayout`, `UsdcRecoveryBurn`)

Both contracts hard-code mainnet addresses as `constant`s; each appears in the creation bytecode only as a
`PUSH20` literal. Replace the 40-hex-character address (lowercase) in the `.bin` with the e2e address and
check the number of occurrences:

| `.bin` | Mainnet constant | Occurrences |
| --- | --- | --- |
| `ZkMinterBurnerPayout` | `BRIDGE` `2a3dd3eb832af982ec71669e178424b10dca2ede` | 3 |
| `ZkMinterBurnerPayout` | `USDCE` `37eaa0ef3549a5bb7d431be78a3d99bd360d19e5` | 3 |
| `ZkMinterBurnerPayout` | `L1_ESCROW` `70e70e58ed7b1cec0d8ef7464072ed8a52d755eb` | 3 |
| `UsdcRecoveryBurn` | `ZK_MINTER_BURNER` `bda0b27f93b2fd3f076725b89cf02e48609bc189` | 5 |

`ZkMinterBurnerPayout` has no `ZK_MINTER_BURNER` and `UsdcRecoveryBurn` none of the other three. The
constructor arguments of `ZkMinterBurnerPayout`, `(address[] chunks, uint256 count, uint256 total)`, are
appended after the substitution. `move.sh.tpl` checks the public getters (`BRIDGE()`, `USDCE()`,
`L1_ESCROW()`, `ZK_MINTER_BURNER()`) after deploying.

```bash
# subst_addr <hex> <old addr> <new addr> <expected occurrences>
subst_addr() {
    local hex="$1" old="${2#0x}" new="${3#0x}" n
    old="${old,,}"; new="${new,,}"
    n="$(grep -o "${old}" <<<"${hex}" | wc -l)"
    [ "${n}" = "$4" ] || { echo "${old}: ${n} occurrence(s), expected $4" >&2; return 1; }
    printf '%s' "${hex//${old}/${new}}"
}
```

## `move.sh.tpl`

The package's `tooling/move.sh.tpl` with every network-specific value as a placeholder; same steps and checks.
It runs where only `cast`, `jq` and `${RPC_URL}` exist, with unlocked or impersonated senders
(`cast send --unlocked --from`). Differences: gas limits are estimated instead of fixed, each transaction is sent with `--async` and its
receipt polled until it is valid (instead of trusting the output of `cast send --json`), the threshold and
the number of signers come from placeholders, and the substituted addresses are checked after deploying.

| Placeholder | Value |
| --- | --- |
| `@@BRIDGE@@` | L2 bridge |
| `@@ZK_MINTER_BURNER@@` | ZkMinterBurner proxy |
| `@@OLD_IMPL@@` | its current implementation (EIP-1967 slot) |
| `@@USDCE@@` | USDC.e proxy |
| `@@USDCE_IMPL@@` | its current implementation (zos slot), restored at the end |
| `@@MULTISEND@@` | MultiSendCallOnly |
| `@@SAFE@@`, `@@SAFE_NONCE@@`, `@@THRESHOLD@@` | the Safe, its current nonce and threshold |
| `@@L1_ESCROW@@` | L1Escrow proxy on L1 |
| `@@EXECUTOR@@` | Safe owner that deploys and executes |
| `@@APPROVERS@@` | `THRESHOLD - 1` other owners, space separated (`approveHash`) |
| `@@COUNT@@`, `@@TOTAL@@` | number of payouts, sum of the amounts (base units) |
| `@@CHUNKS@@` | init codes of the payout-list data contracts, one `    "0x…"` per line |
| `@@IMPL_BYTECODE@@`, `@@BURN_BYTECODE@@` | `0x` + the substituted `.bin` |

Payout list: each data contract's runtime is `0x00 ‖ (holder: 20 bytes ‖ amount: 6 bytes big-endian)*`, at
most 828 entries (under the 24,576-byte code limit), deployed with the 10-byte init prefix
`61 <runtime length: 2 bytes> 80 60 0a 3d 39 3d f3` (PUSH2 len, DUP1, PUSH1 10, RETURNDATASIZE, CODECOPY,
RETURNDATASIZE, RETURN). Given the 6,620 payouts of the package, this function gives the package's 8 init
codes byte for byte.

```bash
# chunk_initcodes < "holder amount" lines: prints one init code (0x…) per data contract.
chunk_initcodes() {
    local data="00" n=0 holder amount
    flush() { printf '0x61%04x80600a3d393df3%s\n' "$(( ${#data} / 2 ))" "${data}"; data="00"; n=0; }
    while read -r holder amount; do
        [ -n "${holder}" ] || continue
        (( amount > 0 && amount < 281474976710656 )) || return 1   # 6 bytes
        holder="${holder#0x}"
        data="${data}${holder,,}$(printf '%012x' "${amount}")"
        n=$(( n + 1 ))
        (( n == 828 )) && flush
    done
    (( n > 0 )) && flush
    return 0
}
# CHUNKS="$(chunk_initcodes < payouts.txt | sed 's/^/    "/; s/$/"/')"
```

## Regenerating

```bash
./build.sh                          # fetch the pinned commits into a temp dir, forge build, compare with the .bin
./build.sh --package <dir>          # also rebuild the two recovery-package contracts from their sources
./build.sh --package <dir> --write  # overwrite the .bin files with the build output
```

It needs git, forge, jq, sha256sum and network access. It exits non-zero when a rebuilt `.bin` differs from the
committed one. The committed `.bin` were produced with forge 1.5.1 (all 13 checked on 2026-11-27); another forge
version may change the metadata hash in the third-party bins (the package bins have no metadata). Only Solidity
sources are copied from the upstreams into Foundry projects that `build.sh` writes; no upstream script, package
manager or Makefile runs.

The two recovery-package contracts (`ZkMinterBurnerPayout`, `UsdcRecoveryBurn`) have no public repository to pin:
they come from the Circle USDC recovery package attached to agglayer/protocol-team-kanban#819 (v2). `--package`
takes a directory holding its `ZkMinterBurnerPayout.sol` and `UsdcRecoveryBurn.sol` (e.g. its `contracts/src/`) and
refuses files whose sha256 differs from the one pinned in `build.sh`; without it, those two `.bin` are not rebuilt.

## Mainnet comparison (2026-11-25)

On-chain runtime code read with `cast code` from public RPCs (zkEVM `https://zkevm-rpc.com`, Ethereum
`https://ethereum-rpc.publicnode.com`), compared with the `deployedBytecode` of these builds, immutables
(`UUPSUpgradeable.__self`) patched in:

| Contract | Address | Result |
| --- | --- | --- |
| ZkMinterBurner impl (1101) | `0xd21AE53dc98aB2668b6d4426e6EA2fFb99a10aE7` | identical except the 32-byte metadata hash |
| ZkMinterBurner proxy (1101) | `0xBDa0B27f93B2FD3f076725b89cf02e48609bC189` | identical except the metadata hash |
| NativeConverter impl (1101) | `0x71498e52c4b96E6376aF6c2Ca10c5657ae4D9e90` | identical except the metadata hash |
| NativeConverter proxy (1101) | `0xd4F3531Fc95572D9e7b9e9328D9FEaa8e8496054` | identical except the metadata hash |
| L1Escrow impl (1) | `0xA4e6762eAAf259DA74696F46fAAF79bA9DdE14E6` (current EIP-1967 implementation) | identical except the metadata hash |
| L1Escrow proxy (1) | `0x70E70e58ed7B1Cec0D8ef7464072ED8A52d755eB` | identical except the metadata hash |
| USDC.e impl (1101) | `0xe0778817d5009D61e8652d97E52cE524b3fD807F` | `FiatTokenV2_1` (v2.1.0, OpenZeppelin 3.1.0, 200 runs): identical except the metadata hash |
| USDC.e proxy (1101) | `0x37eAA0eF3549a5Bb7D431be78a3D99BD360d19e5` | identical except the metadata hash |
| Safe singleton (1101) | `0x3E5c63644E683549055b9Be8653de26E0B4CD36E` (`GnosisSafeL2`, slot 0 of the Safe) | identical except the metadata hash |
| MultiSendCallOnly (1101) | `0x40A2aCCbd92BCA938b02010E17A5b8929b49130D` | identical except the metadata hash |
| GnosisSafeProxyFactory (1101) | `0xa6B71E26C5e0845f74c812102Ca7114b6a896AB2` | identical except the 3 metadata hashes (factory + embedded proxy) |
| GnosisSafeProxy (1101) | `0x2be7b3e7b9BFfbB38B85f563f88A34d84Dc99c9f` | identical except the metadata hash |

So the usdc-lxly sources at `d7cea0d4` match the deployed code: only the metadata hash differs (it covers
source paths and remappings, which depend on the build layout). The usdc-lxly repository also holds the
deployment record (`broadcast/multi/DeployInit.s.sol-latest/run.json`, committed in `cc1261d`, whose
contracts are identical to `d7cea0d4`): its creation bytecode equals these builds except the metadata hash,
and its metadata hashes equal the on-chain ones. Sourcify reports full matches for the usdc-lxly contracts
above (solc 0.8.17, 200 runs, london) and for the USDC.e proxy (solc 0.6.12, 200 runs, istanbul); the USDC.e
implementation is not verified on Sourcify. The mainnet initialization in that record: L2 `initialize(Safe,
Safe, 0x2a3D…2EDe, 0, L1Escrow, USDC.e)` for ZkMinterBurner and NativeConverter (`zkBWUSDC` =
`0xA8CE…C035`), L1 `initialize(owner, admin, 0x2a3D…2EDe, 1, ZkMinterBurner, USDC 0xA0b8…eB48)`.
