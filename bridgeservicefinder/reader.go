package bridgeservicefinder

import (
	"context"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonrollupbaseetrog"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// aggchainMetadataGetter is the minimal aggchainbase surface used to read source #2. It is an
// interface so the concrete binding can be swapped in tests.
type aggchainMetadataGetter interface {
	AggchainMetadata(opts *bind.CallOpts, arg0 string) (string, error)
}

// trustedSequencerURLGetter is the minimal polygonrollupbaseetrog surface used to read source #3.
type trustedSequencerURLGetter interface {
	TrustedSequencerURL(opts *bind.CallOpts) (string, error)
}

// contractReader is the concrete RollupContractReader. It wraps the aggchainbase and
// polygonrollupbaseetrog callers bound to a single rollup contract address and translates their
// low-level call errors into the ErrSourceNotAvailable fall-through sentinel where appropriate.
type contractReader struct {
	aggchainBase aggchainMetadataGetter
	rollupBase   trustedSequencerURLGetter
}

// AggchainMetadata reads aggchainMetadata(key) (source #2). A call error - which on a non-aggchain
// (legacy) rollup means the method is absent or the call reverts, and on any rollup means an ABI
// decode / revert - is mapped to ErrSourceNotAvailable so the resolver falls through to source #3
// rather than aborting. An empty string with a nil error means the key is unset and is returned
// verbatim (the resolver treats it as a fall-through too).
//
// Design note: go-ethereum bindings surface "no such method" and "execution reverted" as ordinary
// errors that are not reliably distinguishable from a transient RPC failure without brittle string
// matching. Because source #2 is optional on legacy rollups by design, this reader takes the safer
// graceful-degradation path and classifies ALL call errors as ErrSourceNotAvailable. A genuinely
// down RPC therefore also falls through to source #3 (which will itself error and, if it is also a
// hard RPC failure, surface as ErrSourceNotAvailable and finally ErrNoSourceAvailable for the
// network). See the S3 summary for the rationale and trade-off.
func (r *contractReader) AggchainMetadata(ctx context.Context, key string) (string, error) {
	url, err := r.aggchainBase.AggchainMetadata(&bind.CallOpts{Context: ctx}, key)
	if err != nil {
		return "", ErrSourceNotAvailable
	}

	return url, nil
}

// TrustedSequencerURL reads trustedSequencerURL() (source #3, before port substitution). A call
// error is mapped to ErrSourceNotAvailable so the resolver treats the source as unavailable and
// yields ErrNoSourceAvailable for the network rather than aborting the whole enumeration.
func (r *contractReader) TrustedSequencerURL(ctx context.Context) (string, error) {
	url, err := r.rollupBase.TrustedSequencerURL(&bind.CallOpts{Context: ctx})
	if err != nil {
		return "", ErrSourceNotAvailable
	}

	return url, nil
}

// newContractReader is the default RollupContractReaderFactory. It binds the aggchainbase and
// polygonrollupbaseetrog callers to addr using the shared eth client (which satisfies
// bind.ContractBackend via BaseEthereumClienter).
func newContractReader(
	addr common.Address, client aggkittypes.BaseEthereumClienter,
) (RollupContractReader, error) {
	aggchainCaller, err := aggchainbase.NewAggchainbaseCaller(addr, client)
	if err != nil {
		return nil, err
	}

	rollupCaller, err := polygonrollupbaseetrog.NewPolygonrollupbaseetrogCaller(addr, client)
	if err != nil {
		return nil, err
	}

	return &contractReader{
		aggchainBase: aggchainCaller,
		rollupBase:   rollupCaller,
	}, nil
}
