import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['test/**/*.test.ts'],
    // The suite is only meaningful against a running server, so the setup owns
    // that server's lifecycle: it builds and starts the Go demo binary before
    // any test runs and kills it afterwards. A clean checkout needs nothing
    // more than `pnpm install && pnpm run generate && pnpm run demo`.
    globalSetup: ['./test/go-server.setup.ts'],
    testTimeout: 30_000,
    // Generous, because the first run compiles the Go binary from cold.
    hookTimeout: 300_000
  }
});
