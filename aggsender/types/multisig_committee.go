package types

import (
	"errors"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
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

// NewSignerInfo creates a new instance of a signer
func NewSignerInfo(url string, address common.Address) *SignerInfo {
	return &SignerInfo{
		URL:     url,
		Address: address,
	}
}

// String returns a string representation of the signer
func (s *SignerInfo) String() string {
	return fmt.Sprintf("{URL: %s, Address: %s}", s.URL, s.Address.Hex())
}

// MultisigCommittee represents a set of authorized signers with a signing threshold.
type MultisigCommittee struct {
	signers    []*SignerInfo
	signersSet map[common.Address]struct{}
	threshold  uint64
}

// NewMultisigCommittee creates a new committee and builds the address set for quick lookup.
func NewMultisigCommittee(signers []*SignerInfo, threshold uint64) (*MultisigCommittee, error) {
	if len(signers) == 0 {
		return nil, errEmptyCommittee
	}

	if threshold == 0 {
		return nil, errZeroThreshold
	}

	if uint64(len(signers)) < threshold {
		return nil, fmt.Errorf("committee size (%d) must be greater than or equal to the signatures threshold (%d)",
			len(signers), threshold)
	}

	committee := &MultisigCommittee{
		threshold:  threshold,
		signers:    make([]*SignerInfo, 0, len(signers)),
		signersSet: make(map[common.Address]struct{}, len(signers)),
	}

	// populate signers
	for _, s := range signers {
		if err := committee.AddSigner(s); err != nil {
			return nil, err
		}
	}

	return committee, nil
}

// AddSigner adds a new signer to the committee.
// Returns an error if the address already exists.
func (m *MultisigCommittee) AddSigner(info *SignerInfo) error {
	if _, exists := m.signersSet[info.Address]; exists {
		return fmt.Errorf("signer %s already in committee", info.String())
	}

	m.signers = append(m.signers, info)
	m.signersSet[info.Address] = struct{}{}
	return nil
}

// ThresholdInt returns the signature threshold required for quorum as an int64.
func (m MultisigCommittee) Threshold() uint64 {
	return m.threshold
}

// Signers returns a shallow copy of the committee's signers slice
// to prevent external modification of the internal slice.
func (m *MultisigCommittee) Signers() []SignerInfo {
	cpy := make([]SignerInfo, len(m.signers))
	for i, s := range m.signers {
		if s != nil {
			cpy[i] = *s
		}
	}
	return cpy
}

// Size returns the committee size
func (m *MultisigCommittee) Size() int {
	return len(m.signers)
}

// IsMember checks if the given address is part of the committee
func (m *MultisigCommittee) IsMember(address common.Address) bool {
	_, exists := m.signersSet[address]
	return exists
}

// String returns a string representation of the committee
func (m *MultisigCommittee) String() string {
	s := "{Committee: {"
	for i, signer := range m.signers {
		s += signer.Address.Hex() + "=" + signer.URL
		if i < len(m.signers)-1 {
			s += ", "
		}
	}
	s += fmt.Sprintf("},  Threshold: %d}", m.threshold)
	return s
}

// ValidationRequest contains all parameters needed for committee validation
type ValidationRequest struct {
	Certificate       *agglayertypes.Certificate
	LastL2BlockInCert uint64
}
