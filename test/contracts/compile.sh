docker run --rm -v $(pwd):/contracts ethereum/solc:0.8.18-alpine - /contracts/verifybatchesmock/VerifyBatchesMock.sol -o /contracts --abi --bin --overwrite --optimize
mv -f VerifyBatchesMock.abi abi/verifybatchesmock.abi
mv -f VerifyBatchesMock.bin bin/verifybatchesmock.bin
rm -f IBasePolygonZkEVMGlobalExitRoot.abi
rm -f IBasePolygonZkEVMGlobalExitRoot.bin
rm -f IPolygonZkEVMGlobalExitRootV2.abi
rm -f IPolygonZkEVMGlobalExitRootV2.bin

docker run --rm -v $(pwd):/contracts ethereum/solc:0.8.18-alpine - /contracts/claimmock/ClaimMock.sol -o /contracts --abi --bin --overwrite --optimize --via-ir
mv -f ClaimMock.abi abi/claimmock.abi
mv -f ClaimMock.bin bin/claimmock.bin

docker run --rm -v $(pwd):/contracts ethereum/solc:0.8.18-alpine - /contracts/claimmockcaller/ClaimMockCaller.sol -o /contracts --abi --bin --overwrite --optimize --via-ir
mv -f ClaimMockCaller.abi abi/claimmockcaller.abi
mv -f ClaimMockCaller.bin bin/claimmockcaller.bin

docker run --rm -v $(pwd):/contracts ethereum/solc:0.8.18-alpine - /contracts/claimmocktest/ClaimMockTest.sol -o /contracts --abi --bin --overwrite --optimize --via-ir
mv -f ClaimMockTest.abi abi/claimmocktest.abi
mv -f ClaimMockTest.bin bin/claimmocktest.bin

docker run --rm -v $(pwd):/contracts ethereum/solc:0.8.18-alpine - /contracts/logemitter/LogEmitter.sol -o /contracts --abi --bin --overwrite --optimize --via-ir
mv -f LogEmitter.abi abi/logemitter.abi
mv -f LogEmitter.bin bin/logemitter.bin

docker run --rm -v $(pwd):/contracts ethereum/solc:0.8.18-alpine - /contracts/mintableerc20/MintableERC20.sol -o /contracts --abi --bin --overwrite --optimize
mv -f MintableERC20.abi abi/mintableerc20.abi
mv -f MintableERC20.bin bin/mintableerc20.bin


rm -f IClaimMock.abi
rm -f IClaimMock.bin
rm -f IClaimMockCaller.abi
rm -f IClaimMockCaller.bin

# BridgeMessageReceiverMock uses pragma ^0.8.20 (newer than the 0.8.18 contracts above) and relies on
# low-level call/abi.encodeCall plus bytes32[32] storage, so it is compiled with a 0.8.20 solc tag and
# --via-ir to avoid stack-too-deep. The committed abi/bin in this repo were extracted from the
# authoritative Foundry artifact at e2e/core/contracts/bridgeAsset/BridgeMessageReceiverMock.json (the
# same artifact the legacy claim-reetrancy.bats deploys); this block documents the reproducible
# from-source path.
docker run --rm -v $(pwd):/contracts ethereum/solc:0.8.20-alpine - /contracts/bridgemessagereceivermock/BridgeMessageReceiverMock.sol -o /contracts --abi --bin --overwrite --optimize --via-ir
mv -f BridgeMessageReceiverMock.abi abi/bridgemessagereceivermock.abi
mv -f BridgeMessageReceiverMock.bin bin/bridgemessagereceivermock.bin
rm -f IBridgeMessageReceiver.abi
rm -f IBridgeMessageReceiver.bin
rm -f IPolygonZkEVMBridgeV2.abi
rm -f IPolygonZkEVMBridgeV2.bin

# InternalClaims uses pragma ^0.8.20 and stores four full claim-parameter sets (each with two
# bytes32[32] arrays), then fires four try/catch claimAsset calls inside onMessageReceived, so it is
# compiled with a 0.8.20 solc tag and --via-ir to avoid stack-too-deep. The committed abi/bin in this
# repo were extracted from the authoritative Foundry artifact at
# e2e/core/contracts/bridgeAsset/InternalClaims.json (the same artifact the legacy internal-claims.bats
# deploys); this block documents the reproducible from-source path.
docker run --rm -v $(pwd):/contracts ethereum/solc:0.8.20-alpine - /contracts/internalclaims/InternalClaims.sol -o /contracts --abi --bin --overwrite --optimize --via-ir
mv -f InternalClaims.abi abi/internalclaims.abi
mv -f InternalClaims.bin bin/internalclaims.bin
rm -f IInternalClaims.abi
rm -f IInternalClaims.bin
rm -f IPolygonZkEVMBridgeV2.abi
rm -f IPolygonZkEVMBridgeV2.bin
