# E2E Snapshot Regeneration Plan

## Issue
June 19 snapshots are broken because they were captured BEFORE L2 became operational:
- L1 only at block 186 (expected 330+)
- L1 state merkle tree incomplete (missing storage proofs)
- L2 not yet producing blocks

## Root Cause
Snapshots were taken based on time/block number, not on L2 operational readiness. June 2 snapshots worked because they were captured after L2 reached functional status.

## Proper Snapshot Capture Criteria
✓ L2 is producing blocks (check via eth_blockNumber on L2 geth)
✓ L2 is finalizing (check consensus layer finalization)
✓ L1 has proper state (check system config storage proofs)
✓ Agglayer is responsive (check health endpoints)

## Snapshot Regeneration Steps

### Phase 1: Start Fresh Kurtosis Environment
```bash
cd ~/repos/0xPolygon/kurtosis-cdk
kurtosis enclave add --enclave-id-prefix aggkit-e2e <preset>
# Wait for initial setup (~5-10 min)
```

### Phase 2: Monitor L2 Operational Status
Monitor until:
1. L2 geth (op-geth-001) produces blocks (block number > 0)
2. Beacon consensus finalizes blocks
3. L1 geth syncs to stable state (block 100+)
4. op-batcher posts batches to L1
5. Agglayer is healthy

Estimated wait: **15-20 minutes**

### Phase 3: Verify System Consistency
Before snapshotting, verify:
```bash
# L2 block production
curl http://localhost:11545 -X POST -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'

# L1 state health  
curl http://localhost:8545 -X POST -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'

# Agglayer health
curl http://localhost:4443/health
```

### Phase 4: Capture Snapshot
```bash
cd ~/repos/0xPolygon/kurtosis-cdk
./snapshot/snapshot.sh <enclave-name> --out ./snapshots
```

The script will:
- Query actual L1 block and state root at capture time
- Extract all service datadirs
- Build docker images
- Update rollup.json with ACTUAL L1 block (not hardcoded 330)

### Phase 5: Verify & Push
```bash
docker images | grep snapshot-geth
# Verify all 15 images created (5 envs × 3 kinds)

# Push to Docker Hub
docker login -u arnaubennassar
docker tag snapshot-geth:<env>-<timestamp> arnaubennassar/snapshot-geth:<env>-<timestamp>
docker push arnaubennassar/snapshot-geth:<env>-<timestamp>
# ... repeat for beacon, validator
```

### Phase 6: Update aggkit docker-compose.yml
Update all 5 environment compose files with new snapshot image tags.

## Success Criteria
✓ All snapshot images pushed to Docker Hub
✓ docker compose up -d --wait succeeds for all 5 envs
✓ op-geth-001 healthy without waiting for arbitrary block height
✓ op-node-001 starts successfully (no storage proof errors)
✓ Aggkit services become healthy
✓ E2E tests pass with <20 minute runtime

## Estimated Timeline
- Phase 1 (setup): ~10 min
- Phase 2 (L2 sync): ~20 min
- Phase 3 (verification): ~5 min
- Phase 4 (snapshot capture): ~5-10 min per environment
- Phase 5 (push): ~15 min (5 envs × 3 services)
- Phase 6 (update): ~5 min
- **Total: ~60-75 minutes**

## Key Difference from June 19 Failure
✗ June 19: Snapshotted at fixed 330 block (L2 not operational)
✓ New approach: Snapshot after L2 operational (whenever L1 reaches it)

The actual L1 block at capture time becomes the new rollup.json.genesis.l1.number - it's derived from reality, not assumed.
