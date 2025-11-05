package types

import (
	"context"
	"math/big"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/l2gersync"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// AggsenderBuilderFlow is an interface that defines the methods to manage the flow of the AggSender
// based on the different prover types
type AggsenderBuilderFlow interface {
	// CheckInitialStatus checks the initial status for the flow it's ok
	CheckInitialStatus(ctx context.Context) error
	// GetCertificateBuildParams returns the parameters to build a certificate
	GetCertificateBuildParams(ctx context.Context) (*CertificateBuildParams, error)
	// BuildCertificate builds a certificate based on the buildParams
	BuildCertificate(ctx context.Context,
		buildParams *CertificateBuildParams) (*agglayertypes.Certificate, error)
	// GenerateBuildParams generates the build parameters based on the preParams
	GenerateBuildParams(ctx context.Context,
		preParams *CertificatePreBuildParams) (*CertificateBuildParams, error)
	// UpdateAggchainData updates the aggchain data field for the given certificate
	UpdateAggchainData(cert *agglayertypes.Certificate, multisig *agglayertypes.Multisig) error
	// Signer is the signer used to sign the certificate
	Signer() signertypes.Signer
}

// AggsenderVerifierFlow is an interface that defines the methods to verify the certificate
type AggsenderVerifierFlow interface {
	// BuildCertificate builds a certificate based on the buildParams
	BuildCertificate(ctx context.Context,
		buildParams *CertificateBuildParams) (*agglayertypes.Certificate, error)
	// GenerateBuildParams generates the build parameters based on the preParams
	GenerateBuildParams(ctx context.Context,
		preParams *CertificatePreBuildParams) (*CertificateBuildParams, error)
	// VerifyCertificate verifies the certificate field for the given certificate
	VerifyCertificate(
		ctx context.Context,
		cert *agglayertypes.Certificate,
		lastBlockInCert uint64,
		lastSettledBlock uint64) error
}

type AggsenderFlowBaser interface {
	GetCertificateBuildParamsInternal(
		ctx context.Context, certType CertificateType) (*CertificateBuildParams, error)
	BuildCertificate(ctx context.Context,
		certParams *CertificateBuildParams,
		lastSentCertificate *CertificateHeader,
		allowEmptyCert bool) (*agglayertypes.Certificate, error)
	GetNewLocalExitRoot(ctx context.Context,
		certParams *CertificateBuildParams) (common.Hash, error)
	VerifyBuildParams(ctx context.Context, fullCert *CertificateBuildParams) error
	VerifyBlockRangeGaps(
		ctx context.Context,
		lastSentCertificate *CertificateHeader,
		newFromBlock, newToBlock uint64) error
	ConvertClaimToImportedBridgeExit(claim bridgesync.Claim) (*agglayertypes.ImportedBridgeExit, error)
	StartL2Block() uint64
	GeneratePreBuildParams(ctx context.Context,
		certType CertificateType) (*CertificatePreBuildParams, error)
	GenerateBuildParams(ctx context.Context,
		preParams CertificatePreBuildParams) (*CertificateBuildParams, error)
	LimitCertSize(certParams *CertificateBuildParams) (*CertificateBuildParams, error)
}

// L1InfoTreeSyncer is an interface defining functions that an L1InfoTreeSyncer should implement
type L1InfoTreeSyncer interface {
	GetInfoByGlobalExitRoot(globalExitRoot common.Hash) (*l1infotreesync.L1InfoTreeLeaf, error)
	GetL1InfoTreeMerkleProofFromIndexToRoot(
		ctx context.Context, index uint32, root common.Hash,
	) (treetypes.Proof, error)
	GetL1InfoTreeRootByIndex(ctx context.Context, index uint32) (treetypes.Root, error)
	GetLastL1InfoTreeRoot(ctx context.Context) (treetypes.Root, error)
	GetProcessedBlockUntil(ctx context.Context, blockNumber uint64) (uint64, common.Hash, error)
	GetInfoByIndex(ctx context.Context, index uint32) (*l1infotreesync.L1InfoTreeLeaf, error)
	GetLatestL1InfoLeafUntilBlock(ctx context.Context, blockNum uint64) (*l1infotreesync.L1InfoTreeLeaf, error)
	IsUpToDate(ctx context.Context, l1Client aggkittypes.BaseEthereumClienter) (bool, error)
}

// L2BridgeSyncer is an interface defining functions that an L2BridgeSyncer should implement
type L2BridgeSyncer interface {
	GetBlockByLER(ctx context.Context, ler common.Hash) (uint64, error)
	GetExitRootByIndex(ctx context.Context, index uint32) (treetypes.Root, error)
	GetBridges(ctx context.Context, fromBlock, toBlock uint64) ([]bridgesync.Bridge, error)
	GetClaims(ctx context.Context, fromBlock, toBlock uint64) ([]bridgesync.Claim, error)
	OriginNetwork() uint32
	GetLastProcessedBlock(ctx context.Context) (uint64, error)
	GetExitRootByHash(ctx context.Context, root common.Hash) (*treetypes.Root, error)
	GetClaimsByGlobalIndex(ctx context.Context, globalIndex *big.Int) ([]bridgesync.Claim, error)
}

// BridgeQuerier is an interface defining functions that an BridgeQuerier should implement
type BridgeQuerier interface {
	GetBridgesAndClaims(
		ctx context.Context,
		fromBlock, toBlock uint64,
	) ([]bridgesync.Bridge, []bridgesync.Claim, error)
	GetExitRootByIndex(ctx context.Context, index uint32) (common.Hash, error)
	GetLastProcessedBlock(ctx context.Context) (uint64, error)
	OriginNetwork() uint32
	WaitForSyncerToCatchUp(ctx context.Context, block uint64) error
	GetUnsetClaimsForBlockRange(ctx context.Context,
		fromBlock, toBlock uint64) ([]bridgesynctypes.Unclaim, error)
}

// ChainGERReader is an interface defining functions that an ChainGERReader should implement
type ChainGERReader interface {
	GetInjectedGERsForRange(ctx context.Context,
		fromBlock, toBlock uint64) (map[common.Hash]l2gersync.GlobalExitRootInfo, error)
	GetRemovedGERsForRange(ctx context.Context,
		fromBlock, toBlock uint64) ([]*agglayertypes.RemovedGER, error)
}

// AgglayerBridgeL2Reader is an interface defining functions that an AgglayerBridgeL2Reader should implement
type AgglayerBridgeL2Reader interface {
	GetUnsetClaimsForBlockRange(ctx context.Context,
		fromBlock, toBlock uint64) ([]bridgesynctypes.Unclaim, error)
}

// L1InfoTreeDataQuerier is an interface defining functions that an L1InfoTreeDataQuerier should implement
// It is used to query data from the L1 Info tree
type L1InfoTreeDataQuerier interface {
	// GetLatestFinalizedL1InfoRoot returns the latest processed l1 info tree root
	// based on the latest finalized l1 block
	GetLatestFinalizedL1InfoRoot(ctx context.Context) (*treetypes.Root, *l1infotreesync.L1InfoTreeLeaf, error)

	// GetFinalizedL1InfoTreeData returns the L1 Info tree data for the last finalized processed block
	// l1InfoTreeData is:
	// - merkle proof of given l1 info tree leaf
	// - the leaf data of the highest index leaf on that block and root
	// - the root of the l1 info tree on that block
	GetFinalizedL1InfoTreeData(ctx context.Context,
	) (treetypes.Proof, *l1infotreesync.L1InfoTreeLeaf, *treetypes.Root, error)

	// GetProofForGER returns the L1 Info tree leaf and the merkle proof for the given GER
	GetProofForGER(ctx context.Context, ger, rootFromWhichToProve common.Hash) (
		*l1infotreesync.L1InfoTreeLeaf, treetypes.Proof, error)

	// CheckIfClaimsArePartOfFinalizedL1InfoTree checks if the claims are part of the finalized L1 Info tree
	CheckIfClaimsArePartOfFinalizedL1InfoTree(
		finalizedL1InfoTreeRoot *treetypes.Root, claims []bridgesync.Claim) error

	// GetL1InfoRootByLeafIndex returns the L1 Info tree root for the given leaf index
	GetL1InfoRootByLeafIndex(ctx context.Context, leafCount uint32) (*treetypes.Root, error)

	// GetInfoByIndex returns the L1 Info tree leaf for the given index
	GetInfoByIndex(ctx context.Context, index uint32) (*l1infotreesync.L1InfoTreeLeaf, error)
}

// GERQuerier is an interface defining functions that an GERQuerier should implement
type GERQuerier interface {
	GetInjectedGERsProofs(
		ctx context.Context,
		finalizedL1InfoTreeRoot *treetypes.Root,
		fromBlock, toBlock uint64) (map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, error)
	GetRemovedGERsForRange(ctx context.Context,
		fromBlock, toBlock uint64) ([]*agglayertypes.RemovedGER, error)
}

// Logger is an interface that defines the methods to log messages
type Logger interface {
	Panicf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
	Info(args ...interface{})
	Infof(format string, args ...interface{})
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
	Warn(args ...interface{})
	Warnf(format string, args ...interface{})
	Debug(args ...interface{})
	Debugf(format string, args ...interface{})
}

// CertificateStatusChecker is an interface defining functions that a CertificateStatusChecker should implement
type CertificateStatusChecker interface {
	//CheckPendingCertificatesStatus(ctx context.Context) CertStatus
	CheckPeriodicallyCertificateStatus(ctx context.Context) (CertStatus, error)
	CheckInitialStatus(
		ctx context.Context,
		delayBetweenRetries time.Duration,
		aggsenderStatus *AggsenderStatus)
}

// RollupDataQuerier is an interface that abstracts interaction with the rollup manager contract
type RollupDataQuerier interface {
	GetRollupData(blockNumber *big.Int) (agglayermanager.AgglayerManagerRollupDataReturn, error)
	GetRollupChainID() (uint64, error)
}

// LERQuerier is an interface defining functions that a Local Exit Root querier should implement
type LERQuerier interface {
	GetLastLocalExitRoot() (common.Hash, error)
}

// MaxL2BlockNumberLimiterInterface is an interface defining functions that a MaxL2BlockNumberLimiter should implement
type MaxL2BlockNumberLimiterInterface interface {
	// AdaptCertificate is a custom handler that adjusts the certificate build parameters
	//  and return it through a new buildParams
	AdaptCertificate(
		buildParams *CertificateBuildParams) (*CertificateBuildParams, error)
}

type VerifyIncomingRequest struct {
	Certificate         *agglayertypes.Certificate
	PreviousCertificate *agglayertypes.CertificateHeader
	LastL2BlockInCert   uint64
}

// HealthCheckStatus defines the status of a health check
type HealthCheckStatus = string

const (
	HealthCheckStatusOK HealthCheckStatus = "OK"
)

// HealthCheckResponse response for health check
type HealthCheckResponse struct {
	Status       HealthCheckStatus
	StatusReason string
	Version      string
}

// IsHealthy checks if the health check response is healthy
func (h *HealthCheckResponse) IsHealthy() bool {
	return h != nil && h.Status == HealthCheckStatusOK
}

// String returns a string representation of the HealthCheckResponse
func (h *HealthCheckResponse) String() string {
	if h == nil {
		return "HealthCheckResponse is nil"
	}
	return "HealthCheckResponse{Status: " + h.Status +
		", StatusReason: " + h.StatusReason +
		", Version: " + h.Version + "}"
}

type CertificateValidator interface {
	ValidateCertificate(ctx context.Context, params VerifyIncomingRequest) error
}

// CertificateValidateAndSigner is an interface to attach a certificate validator and signer
// to aggsender regular flow
type CertificateValidateAndSigner interface {
	// HealthCheck checks the health of the validator service
	HealthCheck(ctx context.Context) (*HealthCheckResponse, error)
	// ValidateAndSignCertificate validates the certificate and signs it if valid.
	ValidateAndSignCertificate(
		ctx context.Context,
		certificate *agglayertypes.Certificate,
		lastL2BlockInCert uint64,
	) ([]byte, error)
	URL() string
	String() string
	Address() common.Address
	Index() uint32
}

// ValidatorClient is an interface defining functions that a ValidatorClient should implement
type ValidatorClient interface {
	HealthCheck(ctx context.Context) (*HealthCheckResponse, error)
	ValidateCertificate(
		ctx context.Context,
		previousCertificateID *common.Hash, // can be nil if there is no previous certificate
		certificate *agglayertypes.Certificate,
		lastL2BlockInCert uint64,
	) ([]byte, error)
}

// LocalExitRootQuery is an interface defining functions that a LocalExitRootQuery should implement
type LocalExitRootQuery interface {
	GetNewLocalExitRoot(ctx context.Context,
		certParams *CertificateBuildParams) (common.Hash, error)
}

// AggchainProofQuerier is an interface defining functions that an AggchainProofQuerier should implement
type AggchainProofQuerier interface {
	GenerateAggchainProof(
		ctx context.Context,
		lastProvenBlock, toBlock uint64,
		certBuildParams *CertificateBuildParams,
	) (*AggchainProof, *treetypes.Root, error)
}

// MultisigContract is an abstraction for Multisig smart contract
type MultisigContract interface {
	Threshold(opts *bind.CallOpts) (*big.Int, error)
	GetAggchainSignerInfos(opts *bind.CallOpts) ([]aggchainbase.IAggchainSignersSignerInfo, error)
	AGGCHAINTYPE(opts *bind.CallOpts) ([2]byte, error)
	CONSENSUSTYPE(opts *bind.CallOpts) (uint32, error)
}

// MultisigQuerier is an abstraction for querying the multisig committee
type MultisigQuerier interface {
	GetMultisigCommittee(ctx context.Context, blockNum *big.Int) (*MultisigCommittee, error)
	ContractMode() (AggsenderMode, error)
	ResolveAutoMode(cfgMode AggsenderMode) (AggsenderMode, error)
}

// ValidatorPoller is an interface defining functions that a ValidatorPoller should implement
type ValidatorPoller interface {
	PollValidators(ctx context.Context, req *ValidationRequest) (*agglayertypes.Multisig, error)
}

// AggchainFEPRollupQuerier is an interface defining functions that an AggchainFEPRollupQuerier should implement
type AggchainFEPRollupQuerier interface {
	StartL2Block() uint64
	GetLastSettledL2Block() (uint64, error)
	IsFEP() bool
}

// CertificateQuerier is an interface defining functions that a CertificateQuerier should implement
type CertificateQuerier interface {
	GetLastSettledCertificateToBlock(
		ctx context.Context,
		cert *agglayertypes.CertificateHeader) (uint64, error)
	GetNewCertificateToBlock(
		ctx context.Context,
		cert *agglayertypes.Certificate) (uint64, error)
	CalculateCertificateType(cert *agglayertypes.Certificate, certToBlock uint64) CertificateType
	CalculateCertificateTypeFromToBlock(certToBlock uint64) CertificateType
}

// FEPContractQuerier is an interface that defines the methods for interacting with the FEP contract.
type FEPContractQuerier interface {
	StartingBlockNumber(opts *bind.CallOpts) (*big.Int, error)
	LatestBlockNumber(opts *bind.CallOpts) (*big.Int, error)
	GetAggchainSigners(opts *bind.CallOpts) ([]common.Address, error)
	OptimisticMode(opts *bind.CallOpts) (bool, error)
	SelectedOpSuccinctConfigName(opts *bind.CallOpts) ([common.HashLength]byte, error)
	OpSuccinctConfigs(opts *bind.CallOpts, arg0 [common.HashLength]byte) (struct {
		AggregationVkey     [common.HashLength]byte
		RangeVkeyCommitment [common.HashLength]byte
		RollupConfigHash    [common.HashLength]byte
	}, error)
}

// OpNodeClienter is an interface that defines the methods for interacting with the OpNode client.
type OpNodeClienter interface {
	OutputAtBlockRoot(blockNum uint64) (common.Hash, error)
}

// AggProofPublicValuesQuerier defines an interface for
// querying aggregation proof public values.
type AggProofPublicValuesQuerier interface {
	GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock uint64,
		l1InfoTreeLeafHash common.Hash) (*AggregationProofPublicValues, error)
}

// FEPInputsQuerier defines an interface for querying FEP inputs required for aggchain proof.
type FEPInputsQuerier interface {
	GetAggchainParams(
		lastProvenBlock, requestedEndBlock uint64,
		l1InfoTreeLeafHash common.Hash) (*AggchainParams, error)
}
