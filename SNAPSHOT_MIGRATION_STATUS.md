# E2E Snapshot Migration Status

**Date**: 2026-06-25  
**Status**: ✅ Code fixes complete | ⏳ Awaiting snapshot regeneration

## Summary

The E2E environment consolidation (op-pp → op-pp-2chains) and snapshot generation fixes are complete. All code has been tested and builds successfully.

## What Was Fixed

### aggkit (E2E Environment)
- **Removed duplicate map key** in `test/e2e/envs/loader.go`
  - EnvOpPP was aliased to "op-pp-2chains" but conflicted with EnvOpPP2Chains
  - Removed EnvOpPP entry from envCapabilities map
  - Build now succeeds ✓

- **Fixed docker-compose for op-pp-2chains** 
  - Removed op-succinct FEP-specific services
  - Removed postgres-001 and op-succinct-proposer-001
  - Now properly contains only OP-Stack services (geth, beacon, validator)

### kurtosis-cdk (Snapshot Generation)
See `~/repos/0xPolygon/kurtosis-cdk/snapshot/SNAPSHOT_FIXES.md` for full details.

Key fixes:
- ✅ Added L2 contract deployment verification (STEP 2c)
- ✅ Fixed hex-to-decimal conversion in block production check
- ✅ New `check-l2-contracts-deployed.sh` script verifies L1 SystemConfig
- ✅ Comprehensive documentation on regeneration

## Current Snapshot Status

**Snapshot**: `aggkit-e2e-op-pp-2chains-20260625-130705`

Status:
- ✓ Proper L1 geth state
- ✓ Valid beacon/validator state  
- ✗ Missing L2 SystemConfig on L1
- ✗ Missing L2 contract initialization data

**Result**: op-node fails to initialize with "failed to verify storage value in storage trie"

This snapshot was generated BEFORE the L2 contract verification step was added. It needs to be regenerated.

## Next Steps

1. **Deploy properly initialized Kurtosis enclave**
   - Ensure L2 is fully initialized and SystemConfig deployed to L1

2. **Generate new snapshot using fixed scripts**
   ```bash
   cd ~/repos/0xPolygon/kurtosis-cdk
   ./snapshot/snapshot.sh aggkit-e2e-op-pp-2chains --out snapshots
   
   # STEP 2c will verify L2 contracts before proceeding
   ```

3. **Build and push images**
   ```bash
   ./snapshot/build-and-push-snapshots.sh arnaubennassar/snapshot
   ```

4. **Update aggkit with new image tags**
   ```bash
   cd ~/repos/agglayer/aggkit
   # Edit test/e2e/envs/op-pp-2chains/docker-compose.yml with new tags
   git add test/e2e/envs/op-pp-2chains/docker-compose.yml
   git commit -m "test(e2e): update snapshot images to latest verified builds"
   ```

5. **Run E2E tests**
   ```bash
   make test-e2e
   # Expected: All tests PASS ✓
   ```

## Files Modified

**aggkit:**
- `test/e2e/envs/loader.go` - Removed duplicate map key
- `test/e2e/envs/op-pp-2chains/docker-compose.yml` - Removed op-succinct services

**kurtosis-cdk:**
- `snapshot/snapshot.sh` - Added STEP 2c verification
- `snapshot/scripts/check-block-production.sh` - Fixed hex conversion
- `snapshot/scripts/check-l2-contracts-deployed.sh` - NEW: L2 contract verification
- `snapshot/SNAPSHOT_FIXES.md` - NEW: Complete documentation

## Building & Testing

Current status:
- ✓ `make build` succeeds
- ✓ No compilation errors
- ⏳ E2E tests blocked by incomplete snapshot

Once snapshot is regenerated with the improved scripts, tests should pass.

## References

- Full snapshot fixes documentation: `/repos/0xPolygon/kurtosis-cdk/snapshot/SNAPSHOT_FIXES.md`
- Commit: `6c5c3c30` - Fixed duplicate key and docker-compose
- Commits: `428ea80a`, `ef0568cd` - Snapshot script improvements (kurtosis-cdk)
