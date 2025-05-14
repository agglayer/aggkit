package db

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/agglayer/aggkit/aggsender/types"
)

var selectQueryCertificateHeader string

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
