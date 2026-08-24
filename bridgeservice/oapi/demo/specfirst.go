package demo

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/agglayer/aggkit/bridgeservice/oapi"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
)

// SpecFirstServer implements the generated oapi.StrictServerInterface over the
// canned rows.
//
// The reason to look at this file is what it does not contain: no JSON tags, no
// marshalling decisions, no choice about how a big integer reaches the wire.
// Those live in the contract, and oapi-codegen rendered them into
// oapi.BridgeResponse. The compiler rejects a handler that returns anything
// else, which is the property the current hand-written service lacks.
type SpecFirstServer struct {
	bridges []*bridgesync.Bridge
}

// NewSpecFirstServer returns a strict server serving the supplied rows.
func NewSpecFirstServer(bridges []*bridgesync.Bridge) *SpecFirstServer {
	return &SpecFirstServer{bridges: bridges}
}

// GetBridges implements the generated strict handler for GET /bridge/v1/bridges.
func (s *SpecFirstServer) GetBridges(
	_ context.Context, request oapi.GetBridgesRequestObject,
) (oapi.GetBridgesResponseObject, error) {
	networkID := uint32(request.Params.NetworkId) //nolint:gosec // demo fixture; ids are small

	responses := make([]oapi.BridgeResponse, 0, len(s.bridges))
	for _, bridge := range s.bridges {
		responses = append(responses, toSpecFirstResponse(bridge, networkID))
	}

	return oapi.GetBridges200JSONResponse{
		Bridges: responses,
		Count:   len(responses),
	}, nil
}

// toSpecFirstResponse maps a synced bridge onto the generated response type. It
// mirrors bridgeservice.NewBridgeResponse field for field, and computes the
// global index with the same bridgesync helper, so any difference between the
// two mounted endpoints comes from the wire format alone and not from the data.
func toSpecFirstResponse(bridge *bridgesync.Bridge, networkID uint32) oapi.BridgeResponse {
	globalIndex, _ := bridgesync.GlobalIndexForBridge(
		bridge.DestinationNetwork, bridge.BlockNum, bridge.DepositCount, networkID, EtrogUpgradeBlock)

	var fromAddress *string
	if bridge.FromAddress != nil {
		hexAddr := bridge.FromAddress.Hex()
		fromAddress = &hexAddr
	}

	return oapi.BridgeResponse{
		BlockNum:       int(bridge.BlockNum), //nolint:gosec // demo fixture; block numbers are small
		BlockPos:       int(bridge.BlockPos), //nolint:gosec // demo fixture
		FromAddress:    fromAddress,
		TxHash:         bridge.TxHash.Hex(),
		GlobalIndex:    types.BigIntString(globalIndex.String()),
		BlockTimestamp: int(bridge.BlockTimestamp), //nolint:gosec // demo fixture
		LeafType:       int(bridge.LeafType),
		OriginNetwork:  int(bridge.OriginNetwork),
		OriginAddress:  bridge.OriginAddress.Hex(),

		DestinationNetwork: int(bridge.DestinationNetwork),
		DestinationAddress: bridge.DestinationAddress.Hex(),
		Amount:             types.BigIntString(bridge.Amount.String()),
		Metadata:           fmt.Sprintf("0x%s", hex.EncodeToString(bridge.Metadata)),
		DepositCount:       int(bridge.DepositCount),
		BridgeHash:         bridge.Hash().Hex(),
		TxnSender:          bridge.TxnSender.Hex(),
		ToAddress:          bridge.ToAddress.Hex(),
	}
}
