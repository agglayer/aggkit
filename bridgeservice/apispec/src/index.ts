// Package barrel. This is what `#schemas` resolves to, which is the module
// specifier baked into the generated client's schema imports — so every schema
// the client references must be exported here under its registered name.
//
// Importing this file also runs schemas.ts's `extendZodAndCodecsWithOpenApi`
// side effect before any caller chains `.openapi(...)`.
export {
  BridgeResponse,
  BridgesResult,
  ErrorResponse,
  GetBridgesQuery
} from './schemas.ts';

export { buildRegistry } from './registry.ts';
