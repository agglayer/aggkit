package db

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashMeddler_PreWrite(t *testing.T) {
	t.Parallel()

	hex := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	hash := common.HexToHash(hex)

	tests := []struct {
		name      string
		fieldPtr  interface{}
		wantValue interface{}
		wantErr   bool
	}{
		{
			name:      "Valid common.Hash",
			fieldPtr:  hash,
			wantValue: hex,
			wantErr:   false,
		},
		{
			name:      "Valid *common.Hash",
			fieldPtr:  &hash,
			wantValue: hex,
			wantErr:   false,
		},
		{
			name:      "Nil *common.Hash",
			fieldPtr:  (*common.Hash)(nil),
			wantValue: []byte{},
			wantErr:   false,
		},
		{
			name:      "Invalid type",
			fieldPtr:  "invalid",
			wantValue: nil,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := HashMeddler{}
			gotValue, err := h.PreWrite(tt.fieldPtr)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantValue, gotValue)
			}
		})
	}
}

type certificateInfo struct {
	Height                  uint64       `meddler:"height"`
	CertificateID           common.Hash  `meddler:"certificate_id,hash"`
	FinalizedL1InfoTreeRoot *common.Hash `meddler:"finalized_l1_info_tree_root,hash"`
}

type certificateInfoBadType struct {
	Height        uint64      `meddler:"height"`
	CertificateID common.Hash `meddler:"certificate_id,hash"`
	// The field is nullable on DB but not in struct
	FinalizedL1InfoTreeRoot common.Hash `meddler:"finalized_l1_info_tree_root,hash"`
}

func TestMeddlerHashPointerIsNull(t *testing.T) {
	db := createExampleDB(t)
	var certificateInfo certificateInfo
	err := meddler.QueryRow(db, &certificateInfo, "SELECT * FROM certificate_info where height=0;")
	require.NoError(t, err, "null case")
	require.Nil(t, certificateInfo.FinalizedL1InfoTreeRoot, "FinalizedL1InfoTreeRoot should be nil for height 0")
	fmt.Print(certificateInfo)

	var badCertificateInfo certificateInfoBadType
	err = meddler.QueryRow(db, &badCertificateInfo, "SELECT * FROM certificate_info where height=0;")
	require.Error(t, err, "bad type case")
	require.ErrorContains(t, err, "converting NULL to string is unsupported")
}

func TestMeddlerHashPointerIsNotNull(t *testing.T) {
	db := createExampleDB(t)
	var certificateInfo certificateInfo
	err := meddler.QueryRow(db, &certificateInfo, "SELECT * FROM certificate_info where height=1;")
	require.NoError(t, err, "data case")
	require.NotNil(t, certificateInfo.FinalizedL1InfoTreeRoot, "FinalizedL1InfoTreeRoot should not be nil for height 1")
	fmt.Print(certificateInfo)
}

func TestMeddlerHashpostReadDoulePtrBadParms(t *testing.T) {
	h := HashMeddler{}
	err := h.postReadDoulePtr(nil, nil)
	require.Error(t, err)
}

func createExampleDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := ":memory:"
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)

	_, err = db.Exec(`
		CREATE TABLE certificate_info (
			height INTEGER PRIMARY KEY,
			certificate_id VARCHAR NOT NULL,
			finalized_l1_info_tree_root VARCHAR
		);
	`)
	require.NoError(t, err, "failed to create table")
	_, err = db.Exec(`
	INSERT INTO certificate_info (height, certificate_id,finalized_l1_info_tree_root) 
	VALUES (0,'0xbeef', NULL);
`)
	require.NoError(t, err, "failed to insert null data")
	_, err = db.Exec(`
		INSERT INTO certificate_info (height,certificate_id, finalized_l1_info_tree_root) 
		VALUES (1, '0xbeef','0x1234567890123456789012345678901234567890');
	`)
	require.NoError(t, err, "failed to insert data")
	return db
}
