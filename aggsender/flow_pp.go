package aggsender

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
)

// ppFlow is a struct that holds the logic for the regular pessimistic proof flow
type ppFlow struct {
	*baseFlow
}

// newPPFlow returns a new instance of the ppFlow
func newPPFlow(log types.Logger,
	cfg Config,
	storage db.AggSenderStorage,
	l1InfoTreeSyncer types.L1InfoTreeSyncer,
	l2Syncer types.L2BridgeSyncer) *ppFlow {
	return &ppFlow{
		baseFlow: &baseFlow{
			log:              log,
			cfg:              cfg,
			l2Syncer:         l2Syncer,
			storage:          storage,
			l1InfoTreeSyncer: l1InfoTreeSyncer,
		},
	}
}

// GetCertificateBuildParams returns the parameters to build a certificate
// this function is the implementation of the FlowManager interface
func (p *ppFlow) GetCertificateBuildParams(ctx context.Context) (*types.CertificateBuildParams, error) {
	buildParams, err := p.getCertificateBuildParamsInternal(ctx)
	if err != nil {
		return nil, err
	}

	if buildParams == nil {
		// no new blocks to send a certificate or no bridges
		return nil, nil
	}

	if len(buildParams.Claims) > 0 {
		var greatestL1InfoTreeIndexUsed uint32

		for _, claim := range buildParams.Claims {
			info, err := p.l1InfoTreeSyncer.GetInfoByGlobalExitRoot(claim.GlobalExitRoot)
			if err != nil {
				return nil, fmt.Errorf("error getting info by global exit root: %s: %w", claim.GlobalExitRoot, err)
			}

			if info.L1InfoTreeIndex > greatestL1InfoTreeIndexUsed {
				greatestL1InfoTreeIndexUsed = info.L1InfoTreeIndex
			}
		}

		rt, err := p.l1InfoTreeSyncer.GetL1InfoTreeRootByIndex(ctx, greatestL1InfoTreeIndexUsed)
		if err != nil {
			return nil, fmt.Errorf("error getting L1 Info tree root by index: %d. Error: %w", greatestL1InfoTreeIndexUsed, err)
		}

		buildParams.L1InfoTreeRootFromWhichToProve = &rt
	}

	return buildParams, nil
}
