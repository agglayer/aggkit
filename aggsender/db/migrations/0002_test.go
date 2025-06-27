package migrations

import (
	"database/sql"
	"path/filepath"
	"runtime"
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

type migrationTester002 struct{}

func (m *migrationTester002) FilenameTemplateDatabase(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get caller information")
	testDir := filepath.Dir(filename)
	path := filepath.Join(testDir, "testdata", "aggsender-001.sqlite")
	return path
}
func (m *migrationTester002) InsertDataBeforeMigrationUp(t *testing.T, db *sql.DB) {
	t.Helper()
}

func (m *migrationTester002) RunAssertsAfterMigrationUp(t *testing.T, db *sql.DB) {
	t.Helper()
	var certificateInfo certificateInfoRowMigration002
	err := meddler.QueryRow(db, &certificateInfo,
		"SELECT * FROM certificate_info WHERE height = $1;", 0)
	require.NoError(t, err)
	require.Nil(t, certificateInfo.FinalizedL1InfoTreeRoot)

}

func (m *migrationTester002) RunAssertsAfterMigrationDown(t *testing.T, db *sql.DB) {
	t.Helper()
	fields, err := dbmigrations.GetTableColumnNames(db, "certificate_info")
	require.NoError(t, err)
	require.NotContains(t, fields, "aggchain_proof")
	require.NotContains(t, fields, "finalized_l1_info_tree_root")
	require.NotContains(t, fields, "l1_info_tree_leaf_count")
}

func TestMigration002(t *testing.T) {
	dbmigrations.TestMigration(t, "aggsender", Migrations, 2, &migrationTester002{})
}
