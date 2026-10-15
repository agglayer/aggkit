package feeestimate

import (
	"github.com/ethereum/go-ethereum/crypto"
)

// Bridge contract event topics the estimator recognizes in sampled transaction receipts.
// The signatures mirror claimsync/downloader.go and bridgesync/downloader.go so the estimator
// and the syncers agree on what counts as a bridge-emitted event log.
var (
	// claimEventTopic is the Etrog-and-later ClaimEvent.
	claimEventTopic = crypto.Keccak256Hash([]byte("ClaimEvent(uint256,uint32,address,address,uint256)"))
	// claimEventPreEtrogTopic is the pre-Etrog ClaimEvent, whose global index was a uint32.
	claimEventPreEtrogTopic = crypto.Keccak256Hash([]byte("ClaimEvent(uint32,uint32,address,address,uint256)"))
	// detailedClaimEventTopic is the sovereign-chain DetailedClaimEvent (AgglayerBridgeL2
	// v12.2.1 and later). claimAsset emits it there alongside ClaimEvent: one claim, two
	// logs. claimsync indexes those claims behind both topics and dedups the pair, so the
	// estimator must recognize all three topics and count the pair once.
	detailedClaimEventTopic = crypto.Keccak256Hash([]byte(
		"DetailedClaimEvent(bytes32[32],bytes32[32]," +
			"uint256,bytes32,bytes32,uint8,uint32," +
			"address,uint32,address,uint256,bytes)"))
	// newWrappedTokenTopic is the NewWrappedToken event the bridge emits while claiming,
	// when it deploys the wrapper for a token that had none: the marker of a first claim.
	newWrappedTokenTopic = crypto.Keccak256Hash([]byte("NewWrappedToken(uint32,address,address,bytes)"))
)
