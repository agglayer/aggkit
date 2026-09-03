/**
 * Emits generated/openapi.yaml from the Zod registry.
 *
 * OpenAPI 3.0 rather than 3.1: oapi-codegen v2 — the Go generator on the other
 * end of this pipeline — reads 3.0 documents, and the demo's whole point is
 * that one artifact feeds both generators.
 */
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { OpenApiGeneratorV3 } from '@asteasolutions/zod-to-openapi';
import { stringify } from 'yaml';

import { buildRegistry } from '#schemas';

const spec = new OpenApiGeneratorV3(buildRegistry().definitions).generateDocument({
  openapi: '3.0.3',
  info: {
    title: 'aggkit bridge service (spec-first demo slice)',
    version: '0.0.0',
    description:
      'Contract-first description of GET /bridge/v1/bridges. Generated from Zod schemas; consumed by oapi-codegen (Go server) and @hey-api/openapi-ts (TypeScript client).'
  },
  servers: [{ url: '/' }]
});

const outPath = resolve(dirname(fileURLToPath(import.meta.url)), '..', 'generated', 'openapi.yaml');
mkdirSync(dirname(outPath), { recursive: true });
writeFileSync(outPath, stringify(spec));
console.log(`Written: ${outPath}`);
