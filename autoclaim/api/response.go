package api

import (
	"encoding/hex"
	"time"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
)

// ErrorResponse is returned when the Auto Claim API cannot complete a request.
type ErrorResponse struct {
	Error string `json:"error" example:"request 0:1:42 not found"`
}

// ListResponse is returned by GET /autoclaim/v1/bridges.
type ListResponse struct {
	Bridges    []RequestResponse `json:"bridges"`
	Count      int               `json:"count"`
	PageNumber uint32            `json:"page_number"`
	PageSize   uint32            `json:"page_size"`
}

// RequestResponse contains stable Auto Claim request status fields.
type RequestResponse struct {
	ID                 string            `json:"id"`
	Status             string            `json:"status"`
	OriginNetwork      uint32            `json:"origin_network"`
	DestinationNetwork uint32            `json:"destination_network"`
	DepositCount       uint32            `json:"deposit_count"`
	GlobalIndex        string            `json:"global_index"`
	BridgeTxHash       string            `json:"bridge_tx_hash"`
	ClaimTxHash        *string           `json:"claim_tx_hash,omitempty"`
	TxManagerID        *string           `json:"tx_manager_id,omitempty"`
	LeafType           uint8             `json:"leaf_type"`
	OriginAddress      string            `json:"origin_address"`
	DestinationAddress string            `json:"destination_address"`
	ToAddress          string            `json:"to_address"`
	TxnSender          string            `json:"txn_sender"`
	Amount             string            `json:"amount"`
	Metadata           string            `json:"metadata"`
	BlockNum           uint64            `json:"block_num"`
	BlockPos           uint64            `json:"block_pos"`
	BlockTimestamp     uint64            `json:"block_timestamp"`
	L1InfoTreeIndex    *uint32           `json:"l1_info_tree_index,omitempty"`
	RetryCount         uint64            `json:"retry_count"`
	MaxRetries         uint64            `json:"max_retries"`
	LastObservedSendAt *time.Time        `json:"last_observed_send_at,omitempty"`
	LastObservedResult *time.Time        `json:"last_observed_result_at,omitempty"`
	PolicyStatus       string            `json:"policy_status,omitempty"`
	PolicyDecision     *DecisionResponse `json:"policy_decision,omitempty"`
	ManualDecision     *DecisionResponse `json:"manual_decision,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	LastError          string            `json:"last_error,omitempty"`
}

// DecisionResponse contains automatic policy or manual decision metadata.
type DecisionResponse struct {
	PolicyName string            `json:"policy_name"`
	Result     string            `json:"result"`
	Reason     string            `json:"reason,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Decider    string            `json:"decider,omitempty"`
	DeciderID  string            `json:"decider_id,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// DecisionRequest is accepted by manual approval and rejection routes.
type DecisionRequest struct {
	Reason    string            `json:"reason" example:"approved by operator"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Decider   string            `json:"decider,omitempty" example:"operator"`
	DeciderID string            `json:"decider_id,omitempty" example:"alice"`
}

func newRequestResponse(request autoclaimtypes.AutoClaimRequest) RequestResponse {
	response := RequestResponse{
		ID:                 string(request.Key),
		Status:             request.Status.String(),
		OriginNetwork:      request.Bridge.OriginNetwork,
		DestinationNetwork: request.Bridge.DestinationNetwork,
		DepositCount:       request.Bridge.DepositCount,
		GlobalIndex:        bigIntString(request.GlobalIndex),
		BridgeTxHash:       request.Bridge.TxHash.Hex(),
		ClaimTxHash:        hashPtrHex(request.ClaimTxHash),
		TxManagerID:        hashPtrHex(request.TxManagerID),
		LeafType:           uint8(request.Bridge.LeafType),
		OriginAddress:      request.Bridge.OriginAddress.Hex(),
		DestinationAddress: request.Bridge.DestinationAddress.Hex(),
		ToAddress:          request.Bridge.ToAddress.Hex(),
		TxnSender:          request.Bridge.TxnSender.Hex(),
		Amount:             bigIntString(request.Bridge.Amount),
		Metadata:           "0x" + hex.EncodeToString(request.Bridge.Metadata),
		BlockNum:           request.Bridge.BlockNum,
		BlockPos:           request.Bridge.BlockPos,
		BlockTimestamp:     request.Bridge.BlockTimestamp,
		L1InfoTreeIndex:    request.L1InfoTreeIndex,
		RetryCount:         request.RetryCount,
		MaxRetries:         request.MaxRetries,
		LastObservedSendAt: request.LastObservedSendAt,
		LastObservedResult: request.LastObservedResultAt,
		CreatedAt:          request.CreatedAt,
		UpdatedAt:          request.UpdatedAt,
		LastError:          request.LastError,
	}
	if request.PolicyDecision != nil {
		response.PolicyStatus = request.PolicyDecision.Result.String()
		response.PolicyDecision = newDecisionResponse(*request.PolicyDecision)
	}
	if request.ManualDecision != nil {
		response.ManualDecision = newDecisionResponse(*request.ManualDecision)
	}
	return response
}

func newDecisionResponse(decision autoclaimtypes.PolicyDecision) *DecisionResponse {
	return &DecisionResponse{
		PolicyName: decision.PolicyName,
		Result:     decision.Result.String(),
		Reason:     decision.Reason,
		Metadata:   decision.Metadata,
		Decider:    decision.Decider,
		DeciderID:  decision.DeciderID,
		CreatedAt:  decision.CreatedAt,
		UpdatedAt:  decision.UpdatedAt,
	}
}
