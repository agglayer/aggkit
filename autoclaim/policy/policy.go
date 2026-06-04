package policy

import (
	"context"
	"time"

	autoclaimconfig "github.com/agglayer/aggkit/autoclaim/config"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
)

const (
	// ReasonAllowAll is used when allow-all approves a request.
	ReasonAllowAll = "allow-all approved"
	// ReasonAPIApprovalRequired is used when api-approve requires a manual API decision.
	ReasonAPIApprovalRequired = "manual API approval required"
	// ReasonMessageClaimsRejected is used when a policy rejects message bridge leaves.
	ReasonMessageClaimsRejected = "message claims are not allowed"
	// ReasonAssetClaimApproved is used when no-message approves an asset bridge leaf.
	ReasonAssetClaimApproved = "asset claim approved"
)

type staticPolicy struct {
	name   autoclaimconfig.PolicyName
	result autoclaimtypes.PolicyResult
	reason string
}

func newStaticPolicy(
	name autoclaimconfig.PolicyName,
	result autoclaimtypes.PolicyResult,
	reason string,
) autoclaimtypes.Policy {
	return staticPolicy{
		name:   name,
		result: result,
		reason: reason,
	}
}

func (p staticPolicy) Evaluate(
	_ context.Context,
	_ autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.PolicyDecision, error) {
	return newDecision(p.name, p.result, p.reason, nil), nil
}

type noMessagePolicy struct{}

func newNoMessagePolicy() autoclaimtypes.Policy {
	return noMessagePolicy{}
}

func (p noMessagePolicy) Evaluate(
	_ context.Context,
	request autoclaimtypes.AutoClaimRequest,
) (*autoclaimtypes.PolicyDecision, error) {
	if request.Bridge.LeafType == bridgesynctypes.LeafTypeMessage {
		return newDecision(
			autoclaimconfig.PolicyNameNoMessage,
			autoclaimtypes.PolicyResultRejected,
			ReasonMessageClaimsRejected,
			nil,
		), nil
	}

	return newDecision(
		autoclaimconfig.PolicyNameNoMessage,
		autoclaimtypes.PolicyResultApproved,
		ReasonAssetClaimApproved,
		nil,
	), nil
}

func newDecision(
	name autoclaimconfig.PolicyName,
	result autoclaimtypes.PolicyResult,
	reason string,
	metadata map[string]string,
) *autoclaimtypes.PolicyDecision {
	now := time.Now().UTC()
	return &autoclaimtypes.PolicyDecision{
		PolicyName: string(name),
		Result:     result,
		Reason:     reason,
		Metadata:   metadata,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}
