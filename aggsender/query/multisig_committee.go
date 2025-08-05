package query

import (
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

var (
	// errEmptyCommittee denotes the empty committee error
	errEmptyCommittee = errors.New("the committee cannot be empty")

	// errZeroThreshold denotes the 0 signatures threshold error
	errZeroThreshold = errors.New("the signatures threshold must be greater than 0")
)

// SignerInfo holds metadata for each signer.
type SignerInfo struct {
	URL     string
	Address common.Address
}

func NewSignerInfo(url string, address common.Address) *SignerInfo {
	return &SignerInfo{URL: url, Address: address}
}

// MultisigCommittee represents a set of authorized signers with a signing threshold.
type MultisigCommittee struct {
	members    []*SignerInfo
	membersSet map[common.Address]struct{}
	threshold  uint32
}

// NewMultisigCommittee creates a new committee and builds the address set for quick lookup.
func NewMultisigCommittee(members []*SignerInfo, threshold uint32) (*MultisigCommittee, error) {
	if len(members) == 0 {
		return nil, errEmptyCommittee
	}

	if threshold == 0 {
		return nil, errZeroThreshold
	}

	if uint32(len(members)) < threshold {
		return nil, fmt.Errorf("committee size (%d) must be greater than or equal to the signatures threshold (%d)",
			len(members), threshold)
	}

	committee := &MultisigCommittee{
		threshold:  threshold,
		members:    make([]*SignerInfo, 0, len(members)),
		membersSet: make(map[common.Address]struct{}, len(members)),
	}

	// populate members
	for _, m := range members {
		if err := committee.AddSigner(m); err != nil {
			return nil, err
		}
	}

	return committee, nil
}

// AddSigner adds a new signer to the committee.
// Returns an error if the address already exists.
func (m *MultisigCommittee) AddSigner(info *SignerInfo) error {
	if _, exists := m.membersSet[info.Address]; exists {
		return fmt.Errorf("signer %s already in committee", info.Address)
	}

	m.members = append(m.members, info)
	m.membersSet[info.Address] = struct{}{}
	return nil
}

// RemoveSigner removes a signer by address.
// Returns an error if not found.
func (m *MultisigCommittee) RemoveSigner(addr common.Address) error {
	if _, exists := m.membersSet[addr]; !exists {
		return fmt.Errorf("signer %s not found in committee", addr)
	}

	if uint32(len(m.membersSet)-1) < m.threshold {
		return fmt.Errorf("cannot remove signer: resulting committee size (%d) would be below threshold (%d)",
			len(m.membersSet)-1, m.threshold)
	}

	// Rebuild member slice without the removed signer
	filtered := make([]*SignerInfo, 0, len(m.members)-1)
	for _, s := range m.members {
		if s.Address != addr {
			filtered = append(filtered, s)
		}
	}

	m.members = filtered
	delete(m.membersSet, addr)
	return nil
}

// HasQuorum checks if the provided signer addresses constitute a valid quorum.
// - Returns an error if any signer is not part of the committee.
// - Duplicate addresses are ignored in counting.
func (m *MultisigCommittee) HasQuorum(signerAddrs []common.Address) (bool, error) {
	seen := make(map[common.Address]struct{}, len(signerAddrs))
	count := uint32(0)

	for _, signerAddr := range signerAddrs {
		if _, exists := m.membersSet[signerAddr]; !exists {
			return false, fmt.Errorf("signer %s is not in the committee", signerAddr)
		}

		// Count each signer only once
		if _, alreadySeen := seen[signerAddr]; !alreadySeen {
			seen[signerAddr] = struct{}{}
			count++
		}
	}

	return count >= m.threshold, nil
}
