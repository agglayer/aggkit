package flows

import (
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
)

var (
	ErrMaxL2BlockNumberExceededInARetryCert = errors.New("maxL2BlockNumberLimiter. " +
		"Max L2 block number exceeded in a retry certificate")
	ErrComplete = errors.New("maxL2BlockNumberLimiter. " +
		"All certs send, no more certificates can be sent")
	ErrBuildParamsIsNil = errors.New("maxL2BlockNumberLimiter. BuildParams is nil")
)

type MaxL2BlockNumberLimiter struct {
	maxL2BlockNumber              uint64
	log                           types.Logger
	allowToResizeRetryCert        bool
	requireOneBridgeInCertificate bool
}

func NewMaxL2BlockNumberLimiter(
	maxL2BlockNumber uint64,
	log types.Logger,
	allowToResizeRetryCert bool,
	requireOneBridgeInCertificate bool,
) *MaxL2BlockNumberLimiter {
	return &MaxL2BlockNumberLimiter{
		maxL2BlockNumber:              maxL2BlockNumber,
		log:                           log,
		allowToResizeRetryCert:        allowToResizeRetryCert,
		requireOneBridgeInCertificate: requireOneBridgeInCertificate,
	}
}

func (f *MaxL2BlockNumberLimiter) IsEnabled() bool {
	return f.maxL2BlockNumber > 0
}

func (f *MaxL2BlockNumberLimiter) IsAllowedBlockNumber(toBlock uint64) bool {
	if !f.IsEnabled() {
		return true
	}
	return toBlock <= f.maxL2BlockNumber
}

func (f *MaxL2BlockNumberLimiter) isUpcomingNextRange(
	fromBlock, toBlock uint64) bool {
	if !f.IsEnabled() {
		return false
	}
	// This is exactly the upcoming next range after the last sent certificate.
	// e.g: max= 150
	// last sent cert: fromBlock= 100, toBlock= 150
	// upcoming next range: fromBlock= 151, toBlock= 200
	return fromBlock == f.maxL2BlockNumber+1 && toBlock > f.maxL2BlockNumber
}

// AdaptCertificate adjusts the certificate build parameters to ensure that
func (f *MaxL2BlockNumberLimiter) AdaptCertificate(
	buildParams *types.CertificateBuildParams) (*types.CertificateBuildParams, error) {
	if !f.IsEnabled() {
		return buildParams, nil
	}
	if buildParams == nil {
		return nil, ErrBuildParamsIsNil
	}

	if f.IsAllowedBlockNumber(buildParams.ToBlock) {
		return buildParams, nil
	}
	f.log.Infof("maxL2BlockNumberLimiter. Adapting certificate build params. "+
		"maxL2BlockNumber: %d, FromBlock: %d, ToBlock: %d",
		f.maxL2BlockNumber, buildParams.FromBlock, buildParams.ToBlock)
	if buildParams.IsARetry() && !f.allowToResizeRetryCert {
		// We have a buildParams that we can't change the range, so it's an error
		return nil, fmt.Errorf("maxL2BlockNumberLimiter can't adapt the retry certificate, "+
			"the ToBlock %d is greater than the maxL2BlockNumber %d. Err: %w",
			buildParams.ToBlock, f.maxL2BlockNumber, ErrMaxL2BlockNumberExceededInARetryCert)
	}

	if f.isUpcomingNextRange(buildParams.FromBlock, buildParams.ToBlock) {
		// we have reach the end, the previous cert was the last one
		return nil, fmt.Errorf("maxL2BlockNumberLimiter finish. The next certificate is just the upcoming next range "+
			"after the last sent certificate. FromBlock: %d, ToBlock: %d, maxL2BlockNumber: %d. Err: %w",
			buildParams.FromBlock, buildParams.ToBlock, f.maxL2BlockNumber, ErrComplete)
	}
	if buildParams.FromBlock > f.maxL2BlockNumber {
		// If the FromBlock is greater than the maxL2BlockNumber, we can't send this certificate
		f.log.Warnf("maxL2BlockNumberLimiter. NextCert is not the upcoming next range, but is far from it."+
			" maxL2BlockNumber: %d, FromBlock: %d. Can be more blocks that expected in a certificate. ",
			f.maxL2BlockNumber, buildParams.FromBlock)
		return nil, fmt.Errorf("maxL2BlockNumberLimiter. Cert has exceeded the maximum block. "+
			"maxL2BlockNumber: %d. but the current buildParams has FromBlock: %d. Err: %w",
			f.maxL2BlockNumber, buildParams.FromBlock, ErrComplete)
	}
	f.log.Infof("maxL2BlockNumberLimiter. Adjusting the certificate build params ToBlock: %d to "+
		"maxL2BlockNumber: %d",
		buildParams.ToBlock, f.maxL2BlockNumber)
	newBuildParams, err := buildParams.Range(buildParams.FromBlock, f.maxL2BlockNumber)
	if err != nil {
		return nil, fmt.Errorf("maxL2BlockNumberLimiter error adjusting the ToBlock of the certificate  %d -> %d: %w",
			buildParams.ToBlock, f.maxL2BlockNumber,
			err)
	}
	if !f.requireOneBridgeInCertificate && newBuildParams.IsEmpty() {
		// If we allow to send a certificate with no bridges, we can return the newBuildParams
		// even if it has no bridges or claims.
		return newBuildParams, nil
	}
	if f.requireOneBridgeInCertificate && newBuildParams.NumberOfBridges() == 0 {
		// Here it's a problem because we cant send this cert, but maybe it's empty
		if newBuildParams.NumberOfClaims() > 0 {
			return nil, fmt.Errorf("maxL2BlockNumberLimiter can't send cert.  maxL2BlockNumber: %d"+
				".but the current reduced range [%d to %d] has no bridges but have %d of ImportedBridges",
				f.maxL2BlockNumber, newBuildParams.FromBlock, newBuildParams.ToBlock, newBuildParams.NumberOfClaims())
		} else {
			f.log.Warnf("Nothing to do. We have submitted all permitted certificate for maxL2BlockNumber: %d",
				f.maxL2BlockNumber)
			return nil, ErrComplete
		}
	}
	return newBuildParams, nil
}
