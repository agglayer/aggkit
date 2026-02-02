package multidownloader

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
)

// RuntimeData is the data that is used to check that the DB is compatible with the runtime data
// basically it contains the relevant data from runtime environment
type RuntimeData struct {
	ChainID   uint64
	Addresses []common.Address
}

func (r RuntimeData) String() string {
	res := fmt.Sprintf("ChainID: %d, Addresses: ", r.ChainID)
	for _, addr := range r.Addresses {
		res += addr.String() + ", "
	}
	return res
}

func (r RuntimeData) IsCompatible(other RuntimeData) error {
	if r.ChainID != other.ChainID {
		return fmt.Errorf("chain ID mismatch: %d != %d", r.ChainID, other.ChainID)
	}
	if len(r.Addresses) != len(other.Addresses) {
		return fmt.Errorf("addresses len mismatch: %d != %d", len(r.Addresses), len(other.Addresses))
	}
	for i, addr := range r.Addresses {
		if addr != other.Addresses[i] {
			return fmt.Errorf("addresses[%d] mismatch: %s != %s", i, addr.String(), other.Addresses[i].String())
		}
	}
	return nil
}
