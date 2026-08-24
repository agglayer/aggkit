/**
 * Registry composition for the demo slice of the aggkit bridge API.
 *
 * `TypedRegistry` accumulates registered operations into its own type as the
 * chain is built, so everything downstream — the OpenAPI document, the
 * generated Go server, the generated TypeScript client — derives from this one
 * value rather than from a hand-maintained parallel description.
 */

import { TypedRegistry } from '@polygonlabs/openapi-registry';

import { addBridgeRoutes } from './routes/bridges.ts';

export const buildRegistry = () => new TypedRegistry().with(addBridgeRoutes);
