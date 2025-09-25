package db

import (
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
	RetainNCertificates     uint32 `mapstructure:"RetainNCertificates"` // 0 = retain all certificates
	KeepCertificatesHistory bool   `mapstructure:"KeepCertificatesHistory"`
}

type StorageRetainCertificatesPolicier interface {
	OnNewCert(tx dbtypes.Querier, storage AggSendeStorageMaintenancer, certKey CertificateKey) error
}

func (r *StorageRetainCertificatesPolicy) OnRetOnNewCertryCert(tx dbtypes.Querier,
	storage AggSendeStorageMaintenancer, certKey CertificateKey) error {
	if certKey.IsRetry() {
		if r.KeepCertificatesHistory {
			return storage.MoveCertificateToHistory(tx, certKey.Height)
		}
		return storage.DeleteCertificate(tx, certKey.Height)
	}
	// Is the first cert for this height
	if r.RetainNCertificates == KeepAllCertificates {
		return nil
	}
	if certKey.Height < uint64(r.RetainNCertificates) {
		return nil
	}
	return storage.DeleteOldCertificates(tx, certKey.Height-uint64(r.RetainNCertificates))
}
