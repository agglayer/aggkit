package db

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectQuery(t *testing.T) {
	query := SelectQuery("certificate_info")
	require.NotEmpty(t, query)

	certHeaderDBFields := getCertificateHeaderDBFieldNames()
	selectQueryFields := getFieldNamesFromQuery(t, query)

	// Check if the fields in the SELECT query match the expected fields
	require.Equal(t, certHeaderDBFields, selectQueryFields)
}

func getFieldNamesFromQuery(t *testing.T, query string) []string {
	t.Helper()

	// Assuming the query is of the form "SELECT field1, field2 FROM table_name"
	start := len("SELECT ")
	end := len(query) - len(" FROM certificate_info")
	fieldPart := query[start:end]
	fields := strings.Split(fieldPart, ", ")
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	return fields
}
