import type { UserConfig } from '@hey-api/openapi-ts';

import { defineRegistryClientConfig } from '@polygonlabs/zod-to-openapi-heyapi';

import { buildRegistry } from '#schemas';

// `defineRegistryClientConfig` locks in the plugin order and flags this
// pipeline depends on -- in particular the registry plugin ahead of
// @hey-api/typescript, so the codec-aware response types win over the
// wire-shape ones.
//
// `schemasFrom` is the specifier baked into the generated client's schema
// imports, so it has to resolve both from the generated code and from inside
// the plugin, which dynamic-imports it to audit that every name it is about to
// emit really exists. The plugin's `await import()` runs from its own location
// under node_modules, so a `#schemas` subpath alias -- the option its README
// suggests for schemas living in the codegen package -- does not resolve; the
// alias is only visible inside the package that declares it. Using the package
// name plus the self-link in package.json devDependencies makes one specifier
// resolve from both places. The payoff is that the generated transformer
// imports the very Zod schemas that produced openapi.yaml, so a response
// violating the contract is rejected by the same code that wrote it.
const config: UserConfig = await defineRegistryClientConfig({
  registry: buildRegistry(),
  schemasFrom: '#schemas',
  input: './generated/openapi.yaml',
  output: { path: './generated/client', clean: true }
});

export default config;
