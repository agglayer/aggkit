import { beforeAll, describe, expect, inject, it } from 'vitest';
import type { ZodIssue } from 'zod';

import { SpecFirstPrefix } from './constants.ts';

import { getBridges, isResponseValidationError } from '../generated/client/index.js';

/**
 * The generated client is the consumer half of the pipeline. It validates every
 * response against the same Zod schemas that produced openapi.yaml, so these
 * two tests are the demonstration in its sharpest form:
 *
 *   - pointed at the endpoint the bridge service serves today, the client
 *     refuses the response, naming global_index;
 *   - pointed at the endpoint generated from the contract, the client accepts
 *     it and hands back exact bigints.
 *
 * Nothing here inspects the raw bytes. That is the point: a consumer written
 * against the published contract cannot tell the difference between "the
 * server is wrong" and "my parser silently rounded" unless something validates.
 */

let baseUrl!: string;

beforeAll(() => {
  baseUrl = inject('demoBaseUrl');
});

/** 2^64 + 5 -- the global index of the first canned row. */
const L1_ORIGIN_GLOBAL_INDEX = 18446744073709551621n;

/** 10^18 -- the amount on that row, also past the 2^53 double-safe range. */
const L1_ORIGIN_AMOUNT = 1000000000000000000n;

describe('the endpoint the service serves today', () => {
  it('is rejected by the generated client, at global_index', async () => {
    const { data, error } = await getBridges({ baseUrl, query: { network_id: 0 } });

    expect(data).equal(undefined);
    // zod-to-openapi-heyapi >= 2.0.4 classifies a 2xx body that fails response
    // validation as a ResponseValidationError (earlier versions misreported it
    // as a TransportError, which this very demo helped surface). The guard is
    // a type predicate, so `cause` (the ZodError) and `body` (the offending
    // post-JSON.parse payload) need no casts.
    if (!isResponseValidationError(error)) {
      throw new Error(`expected ResponseValidationError, got ${String(error)}`);
    }

    const issues: ZodIssue[] = error.cause.issues;
    const globalIndexIssue = issues.find((issue) => issue.path.at(-1) === 'global_index');

    expect(globalIndexIssue).property('code', 'invalid_type');
    expect(globalIndexIssue?.message).contains('expected string, received number');

    // Every row is rejected, not just the first: the encoding is systematic.
    expect(issues.filter((issue) => issue.path.at(-1) === 'global_index')).lengthOf(3);

    // amount is not among the complaints. Its sibling field already uses the
    // string wrapper, which is what makes global_index an oversight rather
    // than a design choice.
    expect(issues.some((issue) => issue.path.at(-1) === 'amount')).equal(false);

    // The rejected body rides on the error for diagnosis -- and it carries the
    // silently-rounded double, which is exactly the corruption being refused.
    const body = error.body as { bridges: Array<{ global_index: unknown }> };
    expect(typeof body.bridges[0]?.global_index).equal('number');
  });
});

describe('the endpoint generated from the contract', () => {
  it('round-trips both big integers as exact bigints', async () => {
    const { data, error } = await getBridges({
      baseUrl: `${baseUrl}${SpecFirstPrefix}`,
      query: { network_id: 0 }
    });

    expect(error).equal(undefined);
    expect(data).property('count', 3);

    const [first] = data?.bridges ?? [];

    expect(first?.global_index).equal(L1_ORIGIN_GLOBAL_INDEX);
    expect(typeof first?.global_index).equal('bigint');
    expect(first?.amount).equal(L1_ORIGIN_AMOUNT);
    expect(typeof first?.amount).equal('bigint');

    // What the round trip is worth: the nearest double to this value is a
    // different number, so a client that read it as a JSON number would be
    // holding the wrong bridge.
    expect(Number(L1_ORIGIN_GLOBAL_INDEX)).not.equal(L1_ORIGIN_GLOBAL_INDEX);
    expect(BigInt(Number(L1_ORIGIN_GLOBAL_INDEX))).equal(18446744073709551616n);
  });
});
