package db

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/agglayer/aggkit/aggsender/types"
)

var (
	selectQueryCertificateHeader string
	errNoCertificateHeader       = fmt.Errorf("missing certificate header")
)

func init() {
	selectQueryCertificateHeader = SelectQuery("certificate_info")
}

// SelectQuery generates a SELECT query string for the CertificateHeader fields using reflection
func SelectQuery(tableName string) string {
	fields := getCertificateHeaderDBFieldNames()

	query := fmt.Sprintf("SELECT %s FROM %s",
		strings.Join(fields, ", "),
		tableName,
	)
	return query
}

// getCertificateHeaderDBFieldNames retrieves a list of database field names
// for the CertificateHeader struct based on the "meddler" struct tags.
// It inspects the struct type using reflection, extracts the "meddler" tag
// values, and collects the field names before any comma in the tag.
// Returns a slice of strings containing the database field names.
func getCertificateHeaderDBFieldNames() []string {
	t := reflect.TypeOf(types.CertificateHeader{})
	fields := []string{}

	for i := range t.NumField() {
		field := t.Field(i)
		meddlerTag := field.Tag.Get("meddler")
		if meddlerTag != "" {
			// Extract the actual field name before any comma
			fieldName := strings.Split(meddlerTag, ",")[0]
			fields = append(fields, fieldName)
		}
	}

	return fields
}

// convertCertificateToCertificateInfo converts a Certificate object from the types package
// into a certificateInfo object. It extracts relevant fields from the Certificate's Header
// and other properties to populate the certificateInfo structure.
// Returns:
//   - A pointer to a certificateInfo object containing the extracted data.
//   - An error if the Certificate's Header is nil.
//
// Errors:
//   - Returns errNoCertificateHeader if the provided Certificate has a nil Header.
func convertCertificateToCertificateInfo(c *types.Certificate) (*certificateInfo, error) {
	if c.Header == nil {
		return nil, errNoCertificateHeader
	}

	return &certificateInfo{
		CertificateID:           c.Header.CertificateID,
		Height:                  c.Header.Height,
		RetryCount:              c.Header.RetryCount,
		PreviousLocalExitRoot:   c.Header.PreviousLocalExitRoot,
		NewLocalExitRoot:        c.Header.NewLocalExitRoot,
		FromBlock:               c.Header.FromBlock,
		ToBlock:                 c.Header.ToBlock,
		Status:                  c.Header.Status,
		CreatedAt:               c.Header.CreatedAt,
		UpdatedAt:               c.Header.UpdatedAt,
		FinalizedL1InfoTreeRoot: c.Header.FinalizedL1InfoTreeRoot,
		L1InfoTreeLeafCount:     c.Header.L1InfoTreeLeafCount,
		CertType:                c.Header.CertType,
		CertSource:              c.Header.CertSource,
		SignedCertificate:       c.SignedCertificate,
		AggchainProof:           c.AggchainProof,
		ExtraData:               c.ExtraData,
	}, nil
}
