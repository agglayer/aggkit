import { z } from 'zod';

import { BigIntegerCodec } from '@polygonlabs/zod-codecs';
import { extendZodAndCodecsWithOpenApi } from '@polygonlabs/zod-codecs/openapi';

// Patches both `ZodType.prototype` and `ZodCodec.prototype` with `.openapi()`.
// Called here, at the top of the only module that defines schemas, so any
// import of this file runs the side effect before a schema is constructed.
// The plain `extendZodWithOpenApi` from @asteasolutions/zod-to-openapi is not
// enough: in zod v4 `ZodCodec` is a sibling of `ZodType`, not a subclass, so
// the upstream patch never reaches an imported codec such as BigIntegerCodec
// and `.openapi(...)` on it throws at module load.
extendZodAndCodecsWithOpenApi(z);

/**
 * Big integers on this API do not fit in a double. `global_index` for an
 * L1-origin bridge is `1 << 64 | depositCount` — 18446744073709551621 for
 * deposit count 5 — and `amount` is a wei-denominated token amount. Emitting
 * either as a bare JSON number silently corrupts it in every JSON.parse-based
 * consumer, so the wire format has to be a quoted decimal string.
 *
 * `BigIntegerCodec` is the wire-string / runtime-bigint pair: it validates a
 * digit string on the wire and hands the caller a real `bigint`. The
 * `x-go-type` extension is what makes the Go side hold the same line —
 * oapi-codegen substitutes aggkit's existing `types.BigIntString` wrapper for
 * the generated field, which marshals as a quoted string and accepts a string
 * or a number on unmarshal.
 *
 * `x-go-type: big.Int` would be the obvious-looking choice and is wrong: a raw
 * `*big.Int` marshals as a bare number and rejects a quoted string on
 * unmarshal, which is exactly the defect this demo exists to prevent.
 *
 * Declared as a function rather than a shared constant because `.openapi()`
 * returns a new schema instance carrying that metadata; each field needs its
 * own description.
 */
const aggkitBigInt = (description: string) =>
  BigIntegerCodec.openapi({
    description,
    'x-go-type': 'types.BigIntString',
    'x-go-type-import': { path: 'github.com/agglayer/aggkit/bridgeservice/types' }
  });

// Export name === registry name throughout this file. The
// @polygonlabs/zod-to-openapi-heyapi plugin emits
// `import { <registeredName> } from '#schemas'` in the generated client and
// audits at codegen time that each name resolves to a Zod export of the same
// name, so renaming an export silently breaks client generation.

/**
 * Mirrors `bridgeservice/types.BridgeResponse`. Field-for-field, including the
 * snake_case wire names and the optional `from_address` (which the Go struct
 * marks `omitempty` and serves as a pointer).
 *
 * The `u32`/`u64` fields are modelled as `z.number().int()` because their real
 * ranges — block heights, network ids, deposit counts, unix timestamps — stay
 * inside the double-safe range. Only the two fields that genuinely exceed
 * 2^53 get the codec treatment.
 */
export const BridgeResponse = z
  .object({
    block_num: z.number().int().nonnegative(),
    block_pos: z.number().int().nonnegative(),
    from_address: z.string().optional(),
    tx_hash: z.string(),
    global_index: aggkitBigInt(
      'Global index of the bridge event (mainnet flag, rollup id and deposit count packed into a 72-bit integer). Exceeds 2^53 for every L1-origin bridge, so it is carried as a decimal string.'
    ),
    block_timestamp: z.number().int().nonnegative(),
    leaf_type: z.number().int().min(0).max(255),
    origin_network: z.number().int().nonnegative(),
    origin_address: z.string(),
    destination_network: z.number().int().nonnegative(),
    destination_address: z.string(),
    amount: aggkitBigInt('Amount of tokens bridged, in the smallest unit of the token.'),
    metadata: z.string(),
    deposit_count: z.number().int().nonnegative(),
    bridge_hash: z.string(),
    txn_sender: z.string(),
    to_address: z.string()
  })
  .openapi('BridgeResponse', { description: 'Detailed information about a bridge event' });

/** Mirrors `bridgeservice/types.BridgesResult`. */
export const BridgesResult = z
  .object({
    bridges: z.array(BridgeResponse),
    count: z.number().int().nonnegative()
  })
  .openapi('BridgesResult', { description: 'Paginated response of bridge events' });

/**
 * Query slot for GET /bridge/v1/bridges. Names are the snake_case ones the
 * existing gin handler reads via `c.Query(...)`, so the migrated route is
 * wire-compatible with the one it replaces.
 *
 * Declared with plain `z.number()` rather than `z.coerce.number()`: the
 * coercing variant has an `unknown` input type in zod v4, which accepts
 * `undefined` and therefore lands in the spec as an optional, nullable
 * parameter — `network_id` would stop being required. String-to-number
 * coercion is the generated server's job here, not the contract's.
 */
export const GetBridgesQuery = z
  .object({
    network_id: z.number().int().nonnegative(),
    page_number: z.number().int().positive().optional(),
    page_size: z.number().int().positive().max(1000).optional(),
    from_address: z.string().optional(),
    deposit_count: z.number().int().nonnegative().optional()
  })
  .openapi('GetBridgesQuery');

/** Mirrors `bridgeservice/types.ErrorResponse`. */
export const ErrorResponse = z
  .object({
    error: z.string()
  })
  .openapi('ErrorResponse', { description: 'Generic error response structure' });
