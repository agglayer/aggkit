package db

import (
	"fmt"

	dbtypes "github.com/agglayer/aggkit/db/types"
)

// Retain policy:
// The DB have two tables: `certificate_info` and `certificate_info_history`
// - certificate_info:
//    this table keep the last certificate for each height
// - certificate_info_history:
//    this table keep retries for a certificate
//    example:
// 	    - save(height 10 / retry 0) -> insert (height 10 / retry 0) INTO certificate_info
// 		- save(height 10 / retry 1) -> move (height 10 / retry 0) INTO certificate_info_history
//                                  -> insert (height 10 / retry 1) INTO certificate_info
//      - save(height 11 / retry 0) -> insert (height 11 / retry 0) INTO certificate_info
//
// So there are two policies to retain certificates:
//   1. retain N (>1 or all) cert and all retries (currently: KeepCertificatesHistory=true)
//   2. retain N (>1 or all) cert for each height no retries (currently: KeepCertificatesHistory=false)
// Two params:
//   - How many certificates to retain
//   - keep history of retries or not
//
// Another ideas:
//   - by size: that could be another of configuring that but maybe is not needed

const (
	KeepAllCertificates = uint32(0)
)

type StorageRetainCertificatesPolicy struct {
	RetainCertificatesCount uint32 `mapstructure:"RetainCertificatesCount"` // 0 = retain all certificates
	KeepCertificatesHistory bool   `mapstructure:"KeepCertificatesHistory"`
}

func (r *StorageRetainCertificatesPolicy) Validate() error {
	if r == nil {
		return fmt.Errorf("retain certificates policy is nil")
	}
	return nil
}

func (r *StorageRetainCertificatesPolicy) String() string {
	if r == nil {
		return "nil"
	}
	var res string
	if r.RetainCertificatesCount == KeepAllCertificates {
		res = "retain all certificates, "
	} else {
		res = fmt.Sprintf("retain last %d certificates, ", r.RetainCertificatesCount)
	}
	return res + fmt.Sprintf("keep history: %t", r.KeepCertificatesHistory)
}

type StorageRetainCertificatesPolicier interface {
	OnNewCert(tx dbtypes.Querier, storage AggSenderStorageMaintainer, certKey CertificateKey) error
}

// NewStorageRetainCertificatesPolicyDefault creates a new StorageRetainCertificatesPolicy with default values
func NewStorageRetainCertificatesPolicyDefault() *StorageRetainCertificatesPolicy {
	return &StorageRetainCertificatesPolicy{
		RetainCertificatesCount: KeepAllCertificates,
		KeepCertificatesHistory: true,
	}
}

// NewStorageRetainCertificatesPolicy creates a new StorageRetainCertificatesPolicy
func NewStorageRetainCertificatesPolicy(retainCertificatesCount uint32,
	keepCertificatesHistory bool) *StorageRetainCertificatesPolicy {
	return &StorageRetainCertificatesPolicy{
		RetainCertificatesCount: retainCertificatesCount,
		KeepCertificatesHistory: keepCertificatesHistory,
	}
}

func (r *StorageRetainCertificatesPolicy) OnNewCert(tx dbtypes.Querier,
	storage AggSenderStorageMaintainer, certKey CertificateKey) error {
	if certKey.IsRetry() {
		if r.KeepCertificatesHistory {
			return storage.MoveCertificateToHistory(tx, certKey.Height)
		}
		return storage.DeleteCertificate(tx, certKey.Height, MaybeDelete)
	}
	// Is the first cert for this height
	if r.RetainCertificatesCount == KeepAllCertificates {
		return nil
	}
	// There are not enough certificates yet to delete
	if certKey.Height < uint64(r.RetainCertificatesCount) {
		return nil
	}
	return storage.DeleteOldCertificates(tx, certKey.Height-uint64(r.RetainCertificatesCount))
}
