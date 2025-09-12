package query

import (
	"context"
	"fmt"
	"math/big"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/converters"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
)

var _ types.CertificateQuerier = (*certificateQuerier)(nil)

// certificateQuerier handles querying for certificate ranges
// settled and pending certificates
type certificateQuerier struct {
	l2BridgeSyncer     types.L2BridgeSyncer
	aggchainFEPQuerier types.AggchainFEPRollupQuerier
	agglayerClient     agglayer.AgglayerClientInterface
}

func NewCertificateQuerier(
	bridgeSyncer types.L2BridgeSyncer,
	aggchainFEPQuerier types.AggchainFEPRollupQuerier,
	agglayerClient agglayer.AgglayerClientInterface,
) types.CertificateQuerier {
	return &certificateQuerier{
		l2BridgeSyncer:     bridgeSyncer,
		aggchainFEPQuerier: aggchainFEPQuerier,
		agglayerClient:     agglayerClient,
	}
}

// GetLastSettledCertificateToBlock determines the highest block number up to which
// certificates have been settled by examining three sources of settlement data.
//
// The method validates that the provided certificate is in Settled status, then
// calculates the maximum block number from:
// 1. The latest settled bridge exit block (from NewLocalExitRoot if present)
// 2. The latest settled imported bridge exit block (from agglayer)
// 3. The last settled L2 block number (from aggchain FEP contract)
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - cert: Certificate header that must be in Settled status
//
// Returns:
//   - uint64: The highest block number representing the last settled certificate boundary
//   - error: If certificate is not settled or any query operation fails
//
// The returned block number represents the safe boundary up to which all
// certificates can be considered settled across all relevant settlement mechanisms.
func (c *certificateQuerier) GetLastSettledCertificateToBlock(
	ctx context.Context,
	cert *agglayertypes.CertificateHeader) (uint64, error) {
	if cert.Status != agglayertypes.Settled {
		return 0, fmt.Errorf("certificate %s is not settled", cert.String())
	}

	var (
		lastBridgeExitBlock         uint64
		lastImportedBridgeExitBlock uint64
		lastSettledL2BlockNum       uint64
		err                         error
	)

	// 1. Get the latest settled bridge exit block number
	// if NewLER is not the first empty LER, it means that the certificate
	// or certificate before it had bridge exits, so we can use it to
	// to determine the last bridge exit block
	lastBridgeExitBlock, err = c.getBlockNumFromLER(ctx, cert.NewLocalExitRoot)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve the bridge exit block number for NewLocalExitRoot %s: %w",
			cert.NewLocalExitRoot.String(), err)
	}

	// 2. Get the latest settled imported bridge exit block number
	networkState, err := c.agglayerClient.GetNetworkInfo(ctx, cert.NetworkID)
	if err != nil {
		return 0, fmt.Errorf("failed to get latest settled imported bridge exit from agglayer: %w", err)
	}

	settledIBE := networkState.SettledImportedBridgeExit
	if settledIBE != nil {
		lastImportedBridgeExitBlock, err = c.getBlockNumFromGlobalIndex(ctx,
			settledIBE.GlobalIndex, settledIBE.BridgeExitHash)
		if err != nil {
			return 0, fmt.Errorf("failed to resolve the block number for last imported bridge exit %s: %w",
				settledIBE.GlobalIndex.String(), err)
		}
	}

	// 3. Get the last settled L2 block number from aggchain FEP contract
	// if network is PP, this will return a 0
	lastSettledL2BlockNum, err = c.aggchainFEPQuerier.GetLastSettledL2Block()
	if err != nil {
		return 0, fmt.Errorf("failed to get last settled L2 block: %w", err)
	}

	// 4. Determine the maximum of the three values which will be the last settled certificate to block
	return max(lastBridgeExitBlock, lastImportedBridgeExitBlock, lastSettledL2BlockNum), nil
}

// GetNewCertificateToBlock determines the new certificate To block based on the
// NewLocalExitRoot and the last imported bridge exit block.
func (c *certificateQuerier) GetNewCertificateToBlock(
	ctx context.Context,
	cert *agglayertypes.Certificate) (uint64, error) {
	var (
		lastBridgeExitBlock         uint64
		lastImportedBridgeExitBlock uint64
		err                         error
	)

	// if NewLER is not the first empty LER, it means that the certificate
	// or certificate before it had bridge exits, so we can use it to
	// to determine the last bridge exit block
	lastBridgeExitBlock, err = c.getBlockNumFromLER(ctx, cert.NewLocalExitRoot)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve the bridge exit block number for NewLocalExitRoot %s: %w",
			cert.NewLocalExitRoot.String(), err)
	}

	if len(cert.ImportedBridgeExits) > 0 {
		// if there are imported bridge exits, we can use the last one to determine the new certificate to block
		lastImportedBridgeExit := cert.ImportedBridgeExits[len(cert.ImportedBridgeExits)-1]
		lastImportedBridgeExitBlock, err = c.getBlockNumFromGlobalIndex(
			ctx, lastImportedBridgeExit.GlobalIndex.ToBigInt(), lastImportedBridgeExit.BridgeExit.Hash())
		if err != nil {
			return 0, fmt.Errorf("failed to resolve the block number for last imported bridge exit %s: %w",
				lastImportedBridgeExit.GlobalIndex.String(), err)
		}
	}

	return max(lastBridgeExitBlock, lastImportedBridgeExitBlock), nil
}

// CalculateCertificateType determines the type of certificate.
// If AggchainData is present, the type is inferred from its variant (PP or FEP).
// Otherwise, it is derived from the last block number in certificate
func (c *certificateQuerier) CalculateCertificateType(
	cert *agglayertypes.Certificate, certToBlock uint64,
) types.CertificateType {
	switch cert.AggchainData.(type) {
	case *agglayertypes.AggchainDataSignature:
		// AggchainDataSignature → PP type
		return types.CertificateTypePP
	case *agglayertypes.AggchainDataMultisig:
		// AggchainDataMultisig → PP type
		return types.CertificateTypePP
	case *agglayertypes.AggchainDataProof:
		// AggchainDataProof → FEP type
		return types.CertificateTypeFEP
	case *agglayertypes.AggchainDataMultisigWithProof:
		// AggchainDataMultisigWithProof → FEP type
		return types.CertificateTypeFEP
	}

	// no AggchainData → fallback on block-based logic
	return c.CalculateCertificateTypeFromToBlock(certToBlock)
}

// CalculateCertificateTypeFromToBlock determines the type of certificate based on the provided ToBlock number
func (c *certificateQuerier) CalculateCertificateTypeFromToBlock(certToBlock uint64) types.CertificateType {
	if c.aggchainFEPQuerier.IsFEP() {
		// if we are in a FEP network, we can determine the type based on the start of the FEP
		if certToBlock < c.aggchainFEPQuerier.StartL2Block() {
			// if the certificate is for a block before the start of the FEP, it is a PP certificate
			return types.CertificateTypePP
		}
		// otherwise, it is a FEP certificate
		return types.CertificateTypeFEP
	}

	return types.CertificateTypePP // Default to PP if not in FEP mode
}

func (c *certificateQuerier) getBlockNumFromLER(ctx context.Context, localExitRoot common.Hash) (uint64, error) {
	if localExitRoot == types.EmptyLER {
		return 0, nil // Empty LER means no exit root, so return 0
	}

	exitRoot, err := c.l2BridgeSyncer.GetExitRootByHash(ctx, localExitRoot)
	if err != nil {
		return 0, fmt.Errorf("failed to get local exit root by hash %s: %w",
			localExitRoot.String(), err)
	}

	return exitRoot.BlockNum, nil
}

func (c *certificateQuerier) getBlockNumFromGlobalIndex(
	ctx context.Context, globalIndex *big.Int, bridgeExitHash common.Hash) (uint64, error) {
	claims, err := c.l2BridgeSyncer.GetClaimsByGlobalIndex(ctx, globalIndex)
	if err != nil {
		return 0, fmt.Errorf("failed to get claim(s) by global index %s: %w", globalIndex.String(), err)
	}

	for _, claim := range claims {
		ibe, err := converters.ConvertToImportedBridgeExitWithoutClaimData(claim)
		if err != nil {
			return 0, fmt.Errorf("failed to convert claim to imported bridge exit: %w", err)
		}

		if ibe.BridgeExit.Hash() == bridgeExitHash {
			// Found the claim with the matching bridge exit hash
			return claim.BlockNum, nil
		}
	}

	// If no claim matches the bridge exit hash, return an error
	return 0, fmt.Errorf("no claim found for bridge exit hash %s", bridgeExitHash.String())
}
