package flows

import (
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
)

var (
	ErrMaxL2BlockNumberExceededInARetryCert = errors.New("featureMaxL2BlockNumber. " +
		"Max L2 block number exceeded in a retry certificate")
	ErrComplete = errors.New("featureMaxL2BlockNumber. " +
		"All certs send, no more certificates can be sent")
	ErrBuildParamsIsNil = errors.New("featureMaxL2BlockNumber. BuildParams is nil")
)

type FeatureMaxL2BlockNumber struct {
	maxL2BlockNumber              uint64
	log                           types.Logger
	allowToResizeRetryCert        bool
	requireOneBridgeInCertificate bool
}

func NewFeatureMaxL2BlockNumber(
	maxL2BlockNumber uint64,
	log types.Logger,
	allowToResizeRetryCert bool,
	requireOneBridgeInCertificate bool,
) *FeatureMaxL2BlockNumber {
	return &FeatureMaxL2BlockNumber{
		maxL2BlockNumber:              maxL2BlockNumber,
		log:                           log,
		allowToResizeRetryCert:        allowToResizeRetryCert,
		requireOneBridgeInCertificate: requireOneBridgeInCertificate,
	}
}

func (f *FeatureMaxL2BlockNumber) IsEnabled() bool {
	return f.maxL2BlockNumber > 0
}

func (f *FeatureMaxL2BlockNumber) IsAllowedBlockNumber(toBlock uint64) bool {
	if !f.IsEnabled() {
		return false
	}
	return toBlock <= f.maxL2BlockNumber
}

func (f *FeatureMaxL2BlockNumber) isUpcomingNextRange(
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
func (f *FeatureMaxL2BlockNumber) AdaptCertificate(
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
	f.log.Infof("featureMaxL2BlockNumber. Adapting certificate build params. "+
		"maxL2BlockNumber: %d, FromBlock: %d, ToBlock: %d",
		f.maxL2BlockNumber, buildParams.FromBlock, buildParams.ToBlock)
	if buildParams.IsARetry() && !f.allowToResizeRetryCert {
		// We have a buildParams that we can't change the range, so it's an error
		return nil, fmt.Errorf("featureMaxL2BlockNumber can't adapt the retry certificate, "+
			"the ToBlock %d is greater than the maxL2BlockNumber %d. Err: %w",
			buildParams.ToBlock, f.maxL2BlockNumber, ErrMaxL2BlockNumberExceededInARetryCert)
	}

	if f.isUpcomingNextRange(buildParams.FromBlock, buildParams.ToBlock) {
		// we have reach the end, the previous cert was the last one
		return nil, fmt.Errorf("featureMaxL2BlockNumber finish. The next certificate is just the upcoming next range "+
			"after the last sent certificate. FromBlock: %d, ToBlock: %d, maxL2BlockNumber: %d. Err: %w",
			buildParams.FromBlock, buildParams.ToBlock, f.maxL2BlockNumber, ErrComplete)
	}
	if buildParams.FromBlock > f.maxL2BlockNumber {
		// If the FromBlock is greater than the maxL2BlockNumber, we can't send this certificate
		f.log.Warnf("featureMaxL2BlockNumber. NextCert is not the upcoming next range, but is far from it."+
			" maxL2BlockNumber: %d, FromBlock: %d. Can be more blocks that expected in a certificate. ",
			f.maxL2BlockNumber, buildParams.FromBlock)
		return nil, fmt.Errorf("featureMaxL2BlockNumber. Cert has exceeded the maximum block. "+
			"maxL2BlockNumber: %d. but the current buildParams has FromBlock: %d. Err: %w",
			f.maxL2BlockNumber, buildParams.FromBlock, ErrComplete)
	}
	f.log.Infof("featureMaxL2BlockNumber. Adjusting the certificate build params ToBlock: %d to "+
		"maxL2BlockNumber: %d",
		buildParams.ToBlock, f.maxL2BlockNumber)
	newBuildParams, err := buildParams.Range(buildParams.FromBlock, f.maxL2BlockNumber)
	if err != nil {
		return nil, fmt.Errorf("featureMaxL2BlockNumber error adjusting the ToBlock of the certificate  %d -> %d: %w",
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
			return nil, fmt.Errorf("featureMaxL2BlockNumber can't send cert.  maxL2BlockNumber: %d"+
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
