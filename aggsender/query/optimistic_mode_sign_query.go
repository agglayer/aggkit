package query

import (
	"crypto/sha256"
	"fmt"
	"log"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/aggchainfep"
	"github.com/agglayer/aggkit/aggsender/grpc"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/opnode"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// OpNodeClienter is an interface that defines the methods for interacting with the OpNode client.
type OpNodeClienter interface {
	OutputAtBlockRoot(blockNum uint64) (common.Hash, error)
}

// The real object that implements OpNodeClienter is opnode.OpNodeClient
var _ OpNodeClienter = (*opnode.OpNodeClient)(nil)

// TrustedSequencerQuerier it's the object that returns the trusted sequencer address that
// usually it's obtanied from rollup contract
type TrustedSequencerQuerier interface {
	TrustedSequencer() (common.Address, error)
}

// The real object that implements TrustedSequencerQuerier is etherman.Client
var _ TrustedSequencerQuerier = (*etherman.Client)(nil)

// FEPContractQuerier is an interface that defines the methods for interacting with the FEP contract.
type FEPContractQuerier interface {
	RollupConfigHash(opts *bind.CallOpts) ([32]byte, error)
}

var _ FEPContractQuerier = (*aggchainfep.Aggchainfep)(nil)

type AggregationProofPublicValues struct {
	l1Head           common.Hash
	l2PreRoot        common.Hash
	claimRoot        common.Hash
	l2BlockNumber    uint64
	rollupConfigHash common.Hash
	multiBlockVKey   common.Hash
	proverAddress    common.Address
}

const abiDefinition = `[
    { "name": "l1Head",           "type": "bytes32" },
    { "name": "l2PreRoot",        "type": "bytes32" },
    { "name": "claimRoot",        "type": "bytes32" },
    { "name": "l2BlockNumber",    "type": "uint64"  },
    { "name": "rollupConfigHash", "type": "bytes32" },
    { "name": "multiBlockVKey",   "type": "bytes32" },
    { "name": "proverAddress",    "type": "address" }
]`

type OptimisticSignatureData struct {
	aggregationProofPublicValues AggregationProofPublicValues
	newLocalExitRoot             common.Hash
	commitImportedBridgeExits    common.Hash
}

func (s *AggregationProofPublicValues) Hash() common.Hash {
	// Crear tipos ABI uno por uno
	tBytes32, err := abi.NewType("bytes32", "", nil)
	if err != nil {
		log.Fatalf("failed to create bytes32 type: %v", err)
	}

	tUint64, err := abi.NewType("uint64", "", nil)
	if err != nil {
		log.Fatalf("failed to create uint64 type: %v", err)
	}

	tAddress, err := abi.NewType("address", "", nil)
	if err != nil {
		log.Fatalf("failed to create address type: %v", err)
	}

	// Definir la secuencia de argumentos ABI
	args := abi.Arguments{
		{Type: tBytes32},
		{Type: tBytes32},
		{Type: tBytes32},
		{Type: tUint64},
		{Type: tBytes32},
		{Type: tBytes32},
		{Type: tAddress},
	}
	packed, err := args.Pack(
		s.l1Head,
		s.l2PreRoot,
		s.claimRoot,
		s.l2BlockNumber,
		s.rollupConfigHash,
		s.multiBlockVKey,
		s.proverAddress,
	)
	if err != nil {
		panic(err)
	}
	shaDigest := sha256.Sum256(packed)
	return shaDigest
	/*return crypto.Keccak256Hash(
		s.l1Head.Bytes(),
		s.l2PreRoot.Bytes(),
		s.claimRoot.Bytes(),
		aggkitcommon.Uint64ToBytes(s.l2BlockNumber),
		s.rollupConfigHash.Bytes(),
		s.multiBlockVKey.Bytes(),
		s.proverAddress.Bytes(),
	)*/
}

func (o *OptimisticSignatureData) Hash() common.Hash {
	aggregationProofPublicValuesHash := o.aggregationProofPublicValues.Hash()
	return crypto.Keccak256Hash(
		aggregationProofPublicValuesHash.Bytes(),
		o.newLocalExitRoot.Bytes(),
		o.commitImportedBridgeExits.Bytes(),
	)
}

func (o *OptimisticSignatureData) Sign(signertypes.HashSigner) common.Hash {
	return common.Hash{}
}

type OptimisticModeSignQuery struct {
	aggchainFEPContract  *aggchainfep.Aggchainfep
	aggchainFEPAddr      common.Address
	opNodeClient         OpNodeClienter
	proverAddressQuerier TrustedSequencerQuerier
	signer               signertypes.Signer
}

func (o *OptimisticModeSignQuery) GetSignatureData(req *grpc.AggchainProofRequest) (*AggregationProofPublicValues, error) {

	l2PreRoot, err := o.opNodeClient.OutputAtBlockRoot(req.LastProvenBlock)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. Fails to get l2PreRoot opNodeClient.OutputAtBlockRoot(%d). Err: %w", req.LastProvenBlock, err)
	}
	claimRoot, err := o.opNodeClient.OutputAtBlockRoot(req.RequestedEndBlock)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. Fails to get claimRoot opNodeClient.OutputAtBlockRoot(%d). Err: %w", req.RequestedEndBlock, err)
	}
	rollupConfigHash, err := o.aggchainFEPContract.RollupConfigHash(nil)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. Fails to get rollupConfigHash from contract %s. Err: %w", o.aggchainFEPAddr, err)
	}
	multiBlockVKey, err := o.aggchainFEPContract.AggregationVkey(nil)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. Fails to get multiBlockVKey(AggregationVkey) from contract %s. Err: %w", o.aggchainFEPAddr, err)
	}
	proverAddress, err := o.proverAddressQuerier.TrustedSequencer()
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. Fails to get proverAddress. Err: %w", err)
	}
	return &AggregationProofPublicValues{
		l1Head:           req.L1InfoTreeLeaf.Hash,
		l2PreRoot:        l2PreRoot,
		claimRoot:        claimRoot,
		l2BlockNumber:    req.RequestedEndBlock,
		rollupConfigHash: rollupConfigHash,
		multiBlockVKey:   multiBlockVKey,
		proverAddress:    proverAddress,
	}, nil
}
