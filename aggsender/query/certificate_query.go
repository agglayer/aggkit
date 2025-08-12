package query

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
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
		return 0, fmt.Errorf("failed to get exit root by hash using NewLocalExitRoot %s: %w",
			cert.NewLocalExitRoot.String(), err)
	}

	// TODO - this might need to be changed once agglayer gives support for this
	// 2. Get the latest settled imported bridge exit block number
	latestSettledIbe, err := c.agglayerClient.GetLatestSettledImportedBridgeExit(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get latest settled imported bridge exit from agglayer: %w", err)
	}

	if latestSettledIbe != nil {
		bigGlobalIndex := latestSettledIbe.ToBigInt()
		claim, err := c.l2BridgeSyncer.GetClaimByGlobalIndex(ctx, bigGlobalIndex)
		if err != nil {
			return 0, fmt.Errorf("failed to get claim by global index %s: %w", bigGlobalIndex.String(), err)
		}

		lastImportedBridgeExitBlock = claim.BlockNum
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
		return 0, fmt.Errorf("failed to get exit root by hash using NewLocalExitRoot %s: %w",
			cert.NewLocalExitRoot.String(), err)
	}

	if len(cert.ImportedBridgeExits) > 0 {
		// if there are imported bridge exits, we can use the last one to determine the new certificate to block
		lastImportedBridgeExit := cert.ImportedBridgeExits[len(cert.ImportedBridgeExits)-1]
		bigGlobalIndex := lastImportedBridgeExit.GlobalIndex.ToBigInt()
		claim, err := c.l2BridgeSyncer.GetClaimByGlobalIndex(ctx, bigGlobalIndex)
		if err != nil {
			return 0, fmt.Errorf("failed to get claim by global index %s: %w", bigGlobalIndex.String(), err)
		}

		lastImportedBridgeExitBlock = claim.BlockNum
	}

	return max(lastBridgeExitBlock, lastImportedBridgeExitBlock), nil
}

// CalculateCertificateType determines the type of certificate based on the last block number in certificate
func (c *certificateQuerier) CalculateCertificateType(certToBlock uint64) types.CertificateType {
	if certToBlock == 0 {
		return types.CertificateTypeUnknown
	}

	if certToBlock < c.aggchainFEPQuerier.StartL2Block() {
		return types.CertificateTypePP
	}

	return types.CertificateTypeFEP
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
