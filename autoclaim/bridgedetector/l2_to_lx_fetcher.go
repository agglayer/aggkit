package bridgedetector

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/bridgeservice/client"
	bridgetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum/common"
)

const base10 = 10

// urlResolver is the subset of bridgeservicefinder.Finder the fetcher needs.
type urlResolver interface {
	GetURL(networkID uint32) (string, error)
}

// candidatesClient is the subset of bridgeservice/client.Client the fetcher needs.
type candidatesClient interface {
	GetClaimCandidates(
		ctx context.Context, params client.GetClaimCandidatesParams,
	) (*bridgetypes.ClaimCandidatesResult, error)
}

// ServiceFetcher adapts a bridgeservicefinder.Finder and per-URL bridge service clients to the
// ClaimCandidatesFetcher interface consumed by the L2ToLx detector.
type ServiceFetcher struct {
	finder    urlResolver
	newClient func(baseURL string) candidatesClient
}

var _ ClaimCandidatesFetcher = (*ServiceFetcher)(nil)

// NewServiceFetcher builds a ClaimCandidatesFetcher backed by the given finder. Each source network's
// bridge service is reached through a client constructed from its resolved URL.
func NewServiceFetcher(finder bridgeservicefinder.Finder) *ServiceFetcher {
	return &ServiceFetcher{
		finder: finder,
		newClient: func(baseURL string) candidatesClient {
			return client.New(client.Config{BaseURL: baseURL})
		},
	}
}

// GetURL resolves the source network's bridge service URL, mapping the finder's not-found error to
// the detector-local ErrURLNotFound.
func (f *ServiceFetcher) GetURL(sourceNetwork uint32) (string, error) {
	url, err := f.finder.GetURL(sourceNetwork)
	if err != nil {
		if errors.Is(err, bridgeservicefinder.ErrURLNotFound) {
			return "", fmt.Errorf("%w: source %d: %w", ErrURLNotFound, sourceNetwork, err)
		}
		return "", err
	}
	return url, nil
}

// GetClaimCandidates fetches one page of claim candidates from the source bridge service reachable at
// query.URL, mapping a "not synced yet" 404 to ErrCandidatesNotSynced and converting the response DTOs
// into domain claim candidates.
func (f *ServiceFetcher) GetClaimCandidates(
	ctx context.Context, query ClaimCandidatesQuery,
) ([]ClaimCandidate, int, error) {
	pageNumber := query.PageNumber
	pageSize := query.PageSize
	params := client.GetClaimCandidatesParams{
		DestinationNetworkIDs: query.DestinationNetworkIDs,
		ToLER:                 query.ToLER.Hex(),
		PageNumber:            &pageNumber,
		PageSize:              &pageSize,
	}
	if query.FromLER != nil {
		fromLER := query.FromLER.Hex()
		params.FromLER = &fromLER
	}

	result, err := f.newClient(query.URL).GetClaimCandidates(ctx, params)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return nil, 0, fmt.Errorf("%w: %w", ErrCandidatesNotSynced, err)
		}
		return nil, 0, err
	}

	candidates := make([]ClaimCandidate, 0, len(result.ClaimCandidates))
	for _, dto := range result.ClaimCandidates {
		candidate, err := toClaimCandidate(dto)
		if err != nil {
			return nil, 0, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, result.Count, nil
}

// toClaimCandidate converts a bridge service claim-candidate DTO into a domain ClaimCandidate. The
// SourceNetwork and GlobalIndex are intentionally left unset here; the detector fills SourceNetwork
// and storage.EnqueueRequest derives the global index and request key.
func toClaimCandidate(dto *bridgetypes.ClaimCandidateResponse) (ClaimCandidate, error) {
	if dto == nil || dto.Bridge == nil {
		return ClaimCandidate{}, fmt.Errorf("autoclaim l2-to-lx: nil claim candidate in response")
	}
	bridge := dto.Bridge

	amount, err := parseAmount(string(bridge.Amount))
	if err != nil {
		return ClaimCandidate{}, fmt.Errorf("autoclaim l2-to-lx: parse candidate amount %q: %w", bridge.Amount, err)
	}
	metadata, err := parseHexBytes(bridge.Metadata)
	if err != nil {
		return ClaimCandidate{}, fmt.Errorf("autoclaim l2-to-lx: parse candidate metadata %q: %w", bridge.Metadata, err)
	}

	var fromAddress *common.Address
	if bridge.FromAddress != nil {
		addr := common.HexToAddress(string(*bridge.FromAddress))
		fromAddress = &addr
	}

	exit := autoclaimtypes.BridgeExit{
		BlockNum:           bridge.BlockNum,
		BlockPos:           bridge.BlockPos,
		FromAddress:        fromAddress,
		TxHash:             common.HexToHash(string(bridge.TxHash)),
		BlockTimestamp:     bridge.BlockTimestamp,
		LeafType:           bridgesynctypes.LeafType(bridge.LeafType),
		OriginNetwork:      bridge.OriginNetwork,
		OriginAddress:      common.HexToAddress(string(bridge.OriginAddress)),
		DestinationNetwork: bridge.DestinationNetwork,
		DestinationAddress: common.HexToAddress(string(bridge.DestinationAddress)),
		Amount:             amount,
		Metadata:           metadata,
		DepositCount:       bridge.DepositCount,
		TxnSender:          common.HexToAddress(string(bridge.TxnSender)),
		ToAddress:          common.HexToAddress(string(bridge.ToAddress)),
	}

	return ClaimCandidate{Bridge: exit}, nil
}

func parseAmount(value string) (*big.Int, error) {
	if value == "" {
		return big.NewInt(0), nil
	}
	amount, ok := new(big.Int).SetString(value, base10)
	if !ok {
		return nil, fmt.Errorf("invalid integer")
	}
	return amount, nil
}

func parseHexBytes(value string) ([]byte, error) {
	trimmed := strings.TrimPrefix(value, "0x")
	if trimmed == "" {
		return nil, nil
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}
