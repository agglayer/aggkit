package db

import (
	"reflect"
	"testing"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/stretchr/testify/require"
)

func TestCertificateFieldsMatchCertificateInfo(t *testing.T) {
	// Check that all fields in CertificateInfo are present in Certificate
	certificateInfoType := reflect.TypeOf(certificateInfo{})

	for i := range certificateInfoType.NumField() {
		field := certificateInfoType.Field(i)
		_, found := reflect.TypeOf(types.CertificateHeader{}).FieldByName(field.Name)
		if !found {
			_, found = reflect.TypeOf(types.Certificate{}).FieldByName(field.Name)
		}
		require.True(t, found, "Field %s is missing in Certificate", field.Name)
	}

	// Check that all fields in Certificate are present in CertificateInfo
	certificateHeaderType := reflect.TypeOf(types.CertificateHeader{})

	// Check that all fields in CertificateHeader are present in CertificateInfo
	for i := range certificateHeaderType.NumField() {
		field := certificateHeaderType.Field(i)
		_, found := certificateInfoType.FieldByName(field.Name)
		require.True(t, found, "Field %s from CertificateHeader is missing in CertificateInfo", field.Name)
	}

	// Check that all fields in Certificate are present in CertificateInfo
	certificateType := reflect.TypeOf(types.Certificate{})

	for i := range certificateType.NumField() {
		field := certificateType.Field(i)
		if field.Name == "Header" {
			continue
		}
		_, found := certificateInfoType.FieldByName(field.Name)
		require.True(t, found, "Field %s from Certificate is missing in CertificateInfo", field.Name)
	}
}
