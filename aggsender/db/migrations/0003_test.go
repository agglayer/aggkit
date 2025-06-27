package migrations

import (
	"context"
	"database/sql"
	"testing"

	dbmigrations "github.com/agglayer/aggkit/db/migrations/testutils"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

type certificateInfoRowMigration002 struct {
	Height        uint64      `meddler:"height"`
	RetryCount    int         `meddler:"retry_count"`
	CertificateID common.Hash `meddler:"certificate_id,hash"`
	// PreviousLocalExitRoot if it's nil means no reported
	PreviousLocalExitRoot   *common.Hash `meddler:"previous_local_exit_root,hash"`
	NewLocalExitRoot        common.Hash  `meddler:"new_local_exit_root,hash"`
	FromBlock               uint64       `meddler:"from_block"`
	ToBlock                 uint64       `meddler:"to_block"`
	Status                  int          `meddler:"status"`
	CreatedAt               uint32       `meddler:"created_at"`
	UpdatedAt               uint32       `meddler:"updated_at"`
	SignedCertificate       *string      `meddler:"signed_certificate"`
	AggchainProof           []byte       `meddler:"aggchain_proof"`
	FinalizedL1InfoTreeRoot *common.Hash `meddler:"finalized_l1_info_tree_root,hash"`
	L1InfoTreeLeafCount     *uint32      `meddler:"l1_info_tree_leaf_count"`
}

type certificateInfoRowMigration003 struct {
	Height        uint64      `meddler:"height"`
	RetryCount    int         `meddler:"retry_count"`
	CertificateID common.Hash `meddler:"certificate_id,hash"`
	// PreviousLocalExitRoot if it's nil means no reported
	PreviousLocalExitRoot   *common.Hash `meddler:"previous_local_exit_root,hash"`
	NewLocalExitRoot        common.Hash  `meddler:"new_local_exit_root,hash"`
	FromBlock               uint64       `meddler:"from_block"`
	ToBlock                 uint64       `meddler:"to_block"`
	Status                  int          `meddler:"status"`
	CreatedAt               uint32       `meddler:"created_at"`
	UpdatedAt               uint32       `meddler:"updated_at"`
	SignedCertificate       *string      `meddler:"signed_certificate"`
	AggchainProof           []byte       `meddler:"aggchain_proof"`
	FinalizedL1InfoTreeRoot *common.Hash `meddler:"finalized_l1_info_tree_root,hash"`
	L1InfoTreeLeafCount     *uint32      `meddler:"l1_info_tree_leaf_count"`
	CertType                string       `meddler:"cert_type"`
	CertSource              string       `meddler:"cert_source"`
	ExtraData               string       `meddler:"extra_data"`
}

var (
	previousLocalExitRoot = common.HexToHash("0x123456")
	signedCertificate10   = "signed_certificate_10"
	migTest003Cert10      = certificateInfoRowMigration002{
		Height:                10,
		RetryCount:            0,
		CertificateID:         common.HexToHash("0x789abc"),
		Status:                4,
		PreviousLocalExitRoot: &previousLocalExitRoot,
		NewLocalExitRoot:      common.HexToHash("0x23456"),
		FromBlock:             1000,
		ToBlock:               2000,
		CreatedAt:             1234,
		UpdatedAt:             1235,
		SignedCertificate:     &signedCertificate10,
		AggchainProof:         []byte{0x01, 0x02, 0x03},
		L1InfoTreeLeafCount:   nil,
	}
)

type migrationTester003 struct{}

func (m *migrationTester003) FilenameTemplateDatabase() string {
	return ""
}
func (m *migrationTester003) InsertDataBeforeMigrationUp(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	err = meddler.Insert(tx, "certificate_info", &migTest003Cert10)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}

func (m *migrationTester003) RunAssertsAfterMigrationUp(t *testing.T, db *sql.DB) {
	t.Helper()
	var certificateInfo certificateInfoRowMigration003
	err := meddler.QueryRow(db, &certificateInfo,
		"SELECT * FROM certificate_info WHERE height = $1;", migTest003Cert10.Height)
	require.NoError(t, err)
	require.Equal(t, migTest003Cert10.Height, certificateInfo.Height)
	require.Equal(t, "", certificateInfo.CertType)
	require.Equal(t, "", certificateInfo.CertSource)
	require.Equal(t, "", certificateInfo.ExtraData)
}

func (m *migrationTester003) RunAssertsAfterMigrationDown(t *testing.T, db *sql.DB) {
	t.Helper()
	fields, err := dbmigrations.GetTableColumnNames(db, "certificate_info")
	require.NoError(t, err)
	require.NotContains(t, fields, "cert_type")
	require.NotContains(t, fields, "cert_source")
	require.NotContains(t, fields, "extra_data")
}

func TestMigration003(t *testing.T) {
	dbmigrations.TestMigration(t, "aggsender", Migrations, 3, &migrationTester003{})
}
