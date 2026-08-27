package types

import "fmt"

// ActivityFilter selects which bridges GetActivity (see domain.ActivityQuerier) returns for a
// from_address, based on ClaimStatus, and doubles as a hint to skip fetching data the caller
// does not want: requesting ActivityFilterPending or ActivityFilterError skips the destination
// bridge service's claim record for a bridge found to be claimed, since it would be filtered
// out of the result anyway — that bridge's cache entry simply stays unsettled and is fetched
// normally the next time a filter that needs it is used (see bridgetracker.ActivityCache.refresh)
type ActivityFilter int

const (
	// ActivityFilterAll returns every bridge found, regardless of claim state (the default)
	ActivityFilterAll ActivityFilter = iota
	// ActivityFilterClaimed returns only bridges confirmed claimed
	ActivityFilterClaimed
	// ActivityFilterPending returns only bridges confirmed still unclaimed (ClaimStatusUnclaimed)
	// — a bridge whose claim state could not be checked is not "pending", see ActivityFilterError
	ActivityFilterPending
	// ActivityFilterError returns only bridges whose isClaimed() check itself failed
	// (ClaimStatusError): their claim state is unknown, neither claimed nor confirmed pending
	ActivityFilterError
)

var activityFilterNames = map[ActivityFilter]string{
	ActivityFilterAll:     "all",
	ActivityFilterClaimed: "claimed",
	ActivityFilterPending: "pending",
	ActivityFilterError:   "error",
}

// String representation of the enum: "all", "claimed", "pending" or "error"
func (f ActivityFilter) String() string {
	if name, ok := activityFilterNames[f]; ok {
		return name
	}
	return fmt.Sprintf("Unknown(%d)", int(f))
}

// ParseActivityFilter parses the filterBridges query parameter: "" (unset) and "all" both mean
// ActivityFilterAll. Returns an error for any other value.
func ParseActivityFilter(s string) (ActivityFilter, error) {
	if s == "" {
		return ActivityFilterAll, nil
	}
	for f, name := range activityFilterNames {
		if name == s {
			return f, nil
		}
	}
	return ActivityFilterAll, fmt.Errorf("invalid filterBridges %q: must be one of all, claimed, pending, error", s)
}
