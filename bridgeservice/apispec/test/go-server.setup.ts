import { spawn, spawnSync, type ChildProcess } from 'node:child_process';
import { createServer } from 'node:net';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';

import type { TestProject } from 'vitest/node';

declare module 'vitest' {
  export interface ProvidedContext {
    demoBaseUrl: string;
  }
}

const repoRoot = resolve(import.meta.dirname, '..', '..', '..');

/**
 * Compile the demo to a binary rather than running `go run`. `go run` starts a
 * second process for the compiled program, so killing it at teardown leaves the
 * server holding the port.
 */
const buildDemoBinary = (outDir: string): string => {
  const binary = join(outDir, 'bridge-specfirst-demo');
  const built = spawnSync('go', ['build', '-o', binary, './bridgeservice/oapi/demo/cmd'], {
    cwd: repoRoot,
    encoding: 'utf8'
  });
  if (built.status !== 0) {
    throw new Error(`go build failed:\n${built.stderr || built.stdout}`);
  }
  return binary;
};

/** Ask the OS for a free port instead of hard-coding one, so parallel runs and
 * a developer's already-running demo server never collide. */
const freePort = async (): Promise<number> =>
  new Promise((resolvePort, reject) => {
    const probe = createServer();
    probe.once('error', reject);
    probe.listen(0, '127.0.0.1', () => {
      const address = probe.address();
      if (address === null || typeof address === 'string') {
        probe.close();
        reject(new Error('could not determine a free port'));
        return;
      }
      const { port } = address;
      probe.close(() => resolvePort(port));
    });
  });

const waitForReady = async (baseUrl: string, child: ChildProcess): Promise<void> => {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`demo server exited early with code ${child.exitCode}`);
    }
    try {
      const response = await fetch(`${baseUrl}/`);
      if (response.ok) return;
    } catch {
      // connection refused while the listener is still coming up
    }
    await sleep(100);
  }
  throw new Error(`demo server did not become ready at ${baseUrl}`);
};

export default async function setup({ provide }: TestProject) {
  const outDir = mkdtempSync(join(tmpdir(), 'bridge-specfirst-demo-'));
  const binary = buildDemoBinary(outDir);
  const port = await freePort();
  const baseUrl = `http://127.0.0.1:${port}`;

  const child = spawn(binary, ['-port', String(port)], { stdio: ['ignore', 'pipe', 'pipe'] });
  child.stderr.on('data', (chunk: Buffer) => process.stderr.write(chunk));

  try {
    await waitForReady(baseUrl, child);
  } catch (err) {
    child.kill('SIGKILL');
    rmSync(outDir, { recursive: true, force: true });
    throw err;
  }

  provide('demoBaseUrl', baseUrl);

  return async () => {
    child.kill('SIGTERM');
    rmSync(outDir, { recursive: true, force: true });
  };
}
