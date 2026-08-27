package types

import "fmt"

// ClaimStatus is the tri-state result of checking a bridge's on-chain claim state (see
// domain.ActivityEntry / the GET /activity/from/{from_address} endpoint): unlike a plain bool,
// it distinguishes "confirmed unclaimed" from "the check itself failed" (e.g. no bridge
// contract address configured for the destination network, or an RPC failure) — a caller must
// not read ClaimStatusError as "not claimed".
type ClaimStatus int

const (
	// ClaimStatusUnclaimed the destination bridge contract's isClaimed() call succeeded and
	// reported the bridge as not yet claimed
	ClaimStatusUnclaimed ClaimStatus = iota
	// ClaimStatusClaimed the destination bridge contract's isClaimed() call succeeded and
	// reported the bridge as claimed
	ClaimStatusClaimed
	// ClaimStatusError the isClaimed() check itself failed; the claim state is unknown and
	// will be retried on the next call
	ClaimStatusError
)

var claimStatusNames = map[ClaimStatus]string{
	ClaimStatusUnclaimed: "false",
	ClaimStatusClaimed:   "true",
	ClaimStatusError:     "error",
}

// String representation of the enum: "false", "true" or "error"
func (s ClaimStatus) String() string {
	if name, ok := claimStatusNames[s]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", int(s))
}
