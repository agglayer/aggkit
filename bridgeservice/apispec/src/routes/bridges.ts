/**
 * The one route this demo migrates: GET /bridge/v1/bridges.
 *
 * Kept in its own module so adding the remaining bridge operations is an
 * append here (or a new `add<Domain>Routes` helper) rather than a rewrite —
 * the same shape `apps-team-ts-template/packages/example-schemas` uses.
 */

import type { RouteWithOpId, TypedRegistry } from '@polygonlabs/openapi-registry';

import { BridgesResult, ErrorResponse, GetBridgesQuery } from '../schemas.ts';

export const addBridgeRoutes = <
  Ops extends Record<string, RouteWithOpId>,
  Schemes extends Record<string, true>
>(
  r: TypedRegistry<Ops, Schemes>
) =>
  r.registerPath({
    operationId: 'getBridges',
    method: 'get',
    path: '/bridge/v1/bridges',
    summary: 'Get bridges',
    description:
      'Returns a paginated list of bridge events for the specified network.',
    tags: ['bridges'],
    request: {
      query: GetBridgesQuery
    },
    responses: {
      200: {
        description: 'Paginated bridge events',
        content: { 'application/json': { schema: BridgesResult } }
      },
      400: {
        description: 'Invalid query parameters',
        content: { 'application/json': { schema: ErrorResponse } }
      },
      // Declared explicitly rather than left to the registry's automatic
      // 5xx injection: that injection uses `@polygonlabs/express`'s canonical
      // error schema, which registers under the same `ErrorResponse` name as
      // aggkit's own and would emit an `allOf` describing a framework aggkit
      // does not run.
      500: {
        description: 'Internal server error',
        content: { 'application/json': { schema: ErrorResponse } }
      }
    }
  });
