package storage

import (
	"context"
	"database/sql"
	"math/big"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/agglayer/aggkit/db"
	logger "github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// newTestStorage creates a new test storage. The returned *sql.DB is the SAME
// connection used internally by the storage, so closing it will cause the storage
// to fail on subsequent operations. This is used for the "db error" tests.
func newTestStorage(t *testing.T) (claimsynctypes.ClaimStorager, *sql.DB) {
	t.Helper()

	lg := logger.GetDefaultLogger()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Open the DB first so we have a handle to share
	rawDB, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)

	// Run migrations manually via NewStandalone on the same path,
	// then use New() to share the rawDB connection.
	// We use NewStandalone to run migrations, then close that storage and reopen with shared DB.
	setupStorage, err := NewStandalone(lg, dbPath, t.Name()+"-setup", 30*time.Second)
	require.NoError(t, err)
	_ = setupStorage // migrations are done; we don't need this handle

	// Now create storage using the shared rawDB
	s, err := New(lg, rawDB, t.Name(), 30*time.Second)
	require.NoError(t, err)

	return s, rawDB
}

// insertBlockAndClaim inserts a block and claim using a transaction.
func insertBlockAndClaim(t *testing.T, ctx context.Context, s claimsynctypes.ClaimStorager, claim claimsynctypes.Claim) {
	t.Helper()

	tx, err := s.NewTx(ctx)
	require.NoError(t, err)

	err = s.InsertBlock(ctx, tx, claim.BlockNum, common.Hash{})
	require.NoError(t, err)

	err = s.InsertClaim(ctx, tx, claim)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())
}

func TestInsertAndGetClaim(t *testing.T) {
	s, _ := newTestStorage(t)
	ctx := context.Background()

	claim := claimsynctypes.Claim{
		BlockNum:    1,
		BlockPos:    0,
		TxHash:      common.HexToHash("0xabc"),
		GlobalIndex: new(big.Int).SetUint64(1093),
		Amount:      big.NewInt(100),
		Type:        claimsynctypes.ClaimEvent,
	}

	insertBlockAndClaim(t, ctx, s, claim)

	got, err := s.GetClaims(ctx, nil, 1, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, claim.GlobalIndex, got[0].GlobalIndex)
	require.Equal(t, claim.BlockNum, got[0].BlockNum)
	require.Equal(t, claim.Type, got[0].Type)
}

func TestGetClaimsByGlobalIndex(t *testing.T) {
	t.Run("claim not found", func(t *testing.T) {
		s, _ := newTestStorage(t)
		ctx := context.Background()

		got, err := s.GetClaimsByGlobalIndex(ctx, nil, big.NewInt(9999))
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("retrieve existing claims", func(t *testing.T) {
		s, _ := newTestStorage(t)
		ctx := context.Background()

		bigIndex := new(big.Int).SetUint64(5000)

		claim1 := claimsynctypes.Claim{
			BlockNum:    1,
			BlockPos:    0,
			GlobalIndex: big.NewInt(1000),
			Amount:      big.NewInt(1),
			Type:        claimsynctypes.ClaimEvent,
		}
		insertBlockAndClaim(t, ctx, s, claim1)

		claim2 := claimsynctypes.Claim{
			BlockNum:            2,
			BlockPos:            0,
			GlobalIndex:         new(big.Int).Set(bigIndex),
			Amount:              big.NewInt(2),
			Metadata:            []byte("meta2"),
			ProofLocalExitRoot:  treetypes.Proof{common.HexToHash("0x1a")},
			ProofRollupExitRoot: treetypes.Proof{common.HexToHash("0x1b")},
			MainnetExitRoot:     common.HexToHash("0x2a"),
			RollupExitRoot:      common.HexToHash("0x2b"),
			GlobalExitRoot:      common.HexToHash("0x2c"),
			Type:                claimsynctypes.ClaimEvent,
		}
		insertBlockAndClaim(t, ctx, s, claim2)

		claim3 := claimsynctypes.Claim{
			BlockNum:            3,
			BlockPos:            0,
			GlobalIndex:         new(big.Int).Set(bigIndex),
			Amount:              big.NewInt(3),
			Metadata:            []byte("meta3"),
			ProofLocalExitRoot:  treetypes.Proof{common.HexToHash("0x9a")},
			ProofRollupExitRoot: treetypes.Proof{common.HexToHash("0x9b")},
			MainnetExitRoot:     common.HexToHash("0x9c"),
			RollupExitRoot:      common.HexToHash("0x9d"),
			GlobalExitRoot:      common.HexToHash("0x9e"),
			Type:                claimsynctypes.ClaimEvent,
		}
		insertBlockAndClaim(t, ctx, s, claim3)

		// Query bigIndex: should return compacted claim (oldest meta + newest proofs)
		got, err := s.GetClaimsByGlobalIndex(ctx, nil, bigIndex)
		require.NoError(t, err)
		require.Len(t, got, 1)

		// Oldest block's metadata
		require.Equal(t, claim2.BlockNum, got[0].BlockNum)
		require.Equal(t, claim2.Metadata, got[0].Metadata)
		// Newest block's proofs
		require.Equal(t, claim3.ProofLocalExitRoot, got[0].ProofLocalExitRoot)
		require.Equal(t, claim3.ProofRollupExitRoot, got[0].ProofRollupExitRoot)
		require.Equal(t, claim3.MainnetExitRoot, got[0].MainnetExitRoot)
	})

	t.Run("large global index", func(t *testing.T) {
		s, _ := newTestStorage(t)
		ctx := context.Background()

		// 2^128 - 1
		largeIndex := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
		claim := claimsynctypes.Claim{
			BlockNum:    1,
			BlockPos:    0,
			GlobalIndex: largeIndex,
			Amount:      big.NewInt(1),
			Type:        claimsynctypes.ClaimEvent,
		}
		insertBlockAndClaim(t, ctx, s, claim)

		got, err := s.GetClaimsByGlobalIndex(ctx, nil, largeIndex)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, 0, got[0].GlobalIndex.Cmp(largeIndex))
	})

	t.Run("zero global index", func(t *testing.T) {
		s, _ := newTestStorage(t)
		ctx := context.Background()

		zeroIndex := big.NewInt(0)
		claim := claimsynctypes.Claim{
			BlockNum:    1,
			BlockPos:    0,
			GlobalIndex: zeroIndex,
			Amount:      big.NewInt(1),
			Type:        claimsynctypes.ClaimEvent,
		}
		insertBlockAndClaim(t, ctx, s, claim)

		got, err := s.GetClaimsByGlobalIndex(ctx, nil, zeroIndex)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, 0, got[0].GlobalIndex.Cmp(zeroIndex))
	})

	t.Run("nil global index", func(t *testing.T) {
		s, _ := newTestStorage(t)
		ctx := context.Background()

		_, err := s.GetClaimsByGlobalIndex(ctx, nil, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "globalIndex cannot be nil")
	})

	t.Run("db error", func(t *testing.T) {
		s, rawDB := newTestStorage(t)
		ctx := context.Background()

		// Close the underlying DB to force an error
		require.NoError(t, rawDB.Close())

		_, err := s.GetClaimsByGlobalIndex(ctx, nil, big.NewInt(1))
		require.Error(t, err)
	})
}

func TestGetClaims_Compact(t *testing.T) {
	// Build a proof with a single distinguishing hash
	makeProof := func(h common.Hash) treetypes.Proof {
		var p treetypes.Proof
		p[0] = h
		return p
	}

	// Define the claims used across test cases.
	// Note: claims[0] and claims[2] are both at block=1 but have different block_pos (0 and 1),
	// since the primary key is (block_num, block_pos).
	buildClaims := func() ([]claimsynctypes.Claim, claimsynctypes.UnsetClaim) {
		claims := []claimsynctypes.Claim{
			{ // claims[0]: block=1, pos=0, gi=1
				BlockNum:            1,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x01"),
				GlobalIndex:         big.NewInt(1),
				Metadata:            []byte("metadata1"),
				ProofLocalExitRoot:  makeProof(common.HexToHash("0x10")),
				ProofRollupExitRoot: makeProof(common.HexToHash("0x11")),
				MainnetExitRoot:     common.HexToHash("0x12"),
				RollupExitRoot:      common.HexToHash("0x13"),
				GlobalExitRoot:      common.HexToHash("0x14"),
				Amount:              big.NewInt(1),
				Type:                claimsynctypes.ClaimEvent,
			},
			{ // claims[1]: block=2, pos=0, gi=2
				BlockNum:            2,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x02"),
				GlobalIndex:         big.NewInt(2),
				Metadata:            []byte("metadata2"),
				ProofLocalExitRoot:  makeProof(common.HexToHash("0x20")),
				ProofRollupExitRoot: makeProof(common.HexToHash("0x21")),
				MainnetExitRoot:     common.HexToHash("0x22"),
				RollupExitRoot:      common.HexToHash("0x23"),
				GlobalExitRoot:      common.HexToHash("0x24"),
				Amount:              big.NewInt(2),
				Type:                claimsynctypes.ClaimEvent,
			},
			{ // claims[2]: block=1, pos=1, gi=100 (oldest for gi=100)
				BlockNum:            1,
				BlockPos:            1,
				TxHash:              common.HexToHash("0x03"),
				GlobalIndex:         big.NewInt(100),
				Metadata:            []byte("original_metadata"),
				ProofLocalExitRoot:  makeProof(common.HexToHash("0x1a")),
				ProofRollupExitRoot: makeProof(common.HexToHash("0x1b")),
				MainnetExitRoot:     common.HexToHash("0x1c"),
				RollupExitRoot:      common.HexToHash("0x1d"),
				GlobalExitRoot:      common.HexToHash("0x1e"),
				Amount:              big.NewInt(3),
				Type:                claimsynctypes.ClaimEvent,
			},
			{ // claims[3]: block=2, pos=1, gi=100 (middle for gi=100)
				BlockNum:            2,
				BlockPos:            1,
				TxHash:              common.HexToHash("0x04"),
				GlobalIndex:         big.NewInt(100),
				Metadata:            []byte("middle_metadata"),
				ProofLocalExitRoot:  makeProof(common.HexToHash("0x2a")),
				ProofRollupExitRoot: makeProof(common.HexToHash("0x2b")),
				MainnetExitRoot:     common.HexToHash("0x2c"),
				RollupExitRoot:      common.HexToHash("0x2d"),
				GlobalExitRoot:      common.HexToHash("0x2e"),
				Amount:              big.NewInt(4),
				Type:                claimsynctypes.ClaimEvent,
			},
			{ // claims[4]: block=3, pos=0, gi=100 (newest for gi=100)
				BlockNum:            3,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x05"),
				GlobalIndex:         big.NewInt(100),
				Metadata:            []byte("newest_metadata"),
				ProofLocalExitRoot:  makeProof(common.HexToHash("0x3a")),
				ProofRollupExitRoot: makeProof(common.HexToHash("0x3b")),
				MainnetExitRoot:     common.HexToHash("0x3c"),
				RollupExitRoot:      common.HexToHash("0x3d"),
				GlobalExitRoot:      common.HexToHash("0x3e"),
				DestinationNetwork:  5,
				Amount:              big.NewInt(5),
				Type:                claimsynctypes.DetailedClaimEvent,
			},
		}

		unsetClaim := claimsynctypes.UnsetClaim{
			BlockNum:    5,
			BlockPos:    0,
			TxHash:      common.HexToHash("0xaa"),
			GlobalIndex: big.NewInt(100),
		}

		return claims, unsetClaim
	}

	insertClaims := func(t *testing.T, s claimsynctypes.ClaimStorager, ctx context.Context, toInsert []claimsynctypes.Claim) {
		t.Helper()
		// Track inserted blocks to avoid duplicates
		insertedBlocks := map[uint64]bool{}
		for _, c := range toInsert {
			if !insertedBlocks[c.BlockNum] {
				err := s.InsertBlock(ctx, nil, c.BlockNum, common.Hash{})
				require.NoError(t, err)
				insertedBlocks[c.BlockNum] = true
			}
			err := s.InsertClaim(ctx, nil, c)
			require.NoError(t, err)
		}
	}

	t.Run("single claim, no compaction", func(t *testing.T) {
		s, _ := newTestStorage(t)
		ctx := context.Background()
		claims, _ := buildClaims()

		insertClaims(t, s, ctx, claims[:1])

		got, err := s.GetClaims(ctx, nil, 1, 1)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, claims[0].GlobalIndex, got[0].GlobalIndex)
		require.Equal(t, claims[0].Metadata, got[0].Metadata)
	})

	t.Run("two distinct global indexes, no compaction", func(t *testing.T) {
		s, _ := newTestStorage(t)
		ctx := context.Background()
		claims, _ := buildClaims()

		insertClaims(t, s, ctx, claims[:2])

		got, err := s.GetClaims(ctx, nil, 1, 2)
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.Equal(t, claims[0].GlobalIndex, got[0].GlobalIndex)
		require.Equal(t, claims[1].GlobalIndex, got[1].GlobalIndex)
	})

	t.Run("compact three claims with same global index", func(t *testing.T) {
		s, _ := newTestStorage(t)
		ctx := context.Background()
		claims, _ := buildClaims()

		// Insert claims[2], claims[3], claims[4] — all gi=100
		insertClaims(t, s, ctx, []claimsynctypes.Claim{claims[2], claims[3], claims[4]})

		got, err := s.GetClaims(ctx, nil, 1, 3)
		require.NoError(t, err)
		require.Len(t, got, 1)

		// Oldest metadata (block 1, pos 1 = claims[2])
		require.Equal(t, claims[2].BlockNum, got[0].BlockNum)
		require.Equal(t, claims[2].BlockPos, got[0].BlockPos)
		require.Equal(t, claims[2].Metadata, got[0].Metadata)
		require.Equal(t, claims[2].TxHash, got[0].TxHash)
		require.Equal(t, claims[2].Amount, got[0].Amount)
		// Type comes from oldest (claims[2] = ClaimEvent)
		require.Equal(t, claims[2].Type, got[0].Type)

		// Newest proofs (block 3 = claims[4])
		require.Equal(t, claims[4].ProofLocalExitRoot, got[0].ProofLocalExitRoot)
		require.Equal(t, claims[4].ProofRollupExitRoot, got[0].ProofRollupExitRoot)
		require.Equal(t, claims[4].MainnetExitRoot, got[0].MainnetExitRoot)
		require.Equal(t, claims[4].RollupExitRoot, got[0].RollupExitRoot)
		require.Equal(t, claims[4].GlobalExitRoot, got[0].GlobalExitRoot)
		// DestinationNetwork comes from oldest (claims[2]), not newest (claims[4])
		require.Equal(t, claims[2].DestinationNetwork, got[0].DestinationNetwork)
	})

	t.Run("no compaction when unset_claim exists", func(t *testing.T) {
		s, _ := newTestStorage(t)
		ctx := context.Background()
		claims, unsetClaim := buildClaims()

		// Insert claims[2]+claims[3]+claims[4] and an unset_claim for gi=100
		insertClaims(t, s, ctx, []claimsynctypes.Claim{claims[2], claims[3], claims[4]})

		// Insert block 5 and then the unset_claim
		require.NoError(t, s.InsertBlock(ctx, nil, 5, common.Hash{}))
		require.NoError(t, s.InsertUnsetClaim(ctx, nil, unsetClaim))

		// GetClaims for blocks 1-3: all 3 claims should be returned uncompacted
		got, err := s.GetClaims(ctx, nil, 1, 3)
		require.NoError(t, err)
		require.Len(t, got, 3)
	})

	t.Run("query range excludes some blocks", func(t *testing.T) {
		s, _ := newTestStorage(t)
		ctx := context.Background()
		claims, _ := buildClaims()

		// Insert: claims[0](b1,p0,gi=1), claims[2](b1,p1,gi=100), claims[1](b2,p0,gi=2),
		//         claims[3](b2,p1,gi=100), claims[4](b3,p0,gi=100)
		insertClaims(t, s, ctx, []claimsynctypes.Claim{claims[0], claims[2], claims[1], claims[3], claims[4]})

		// GetClaims for block range [2,2]:
		// all_claims_ranked ranks over ALL claims in DB:
		//   gi=1: claims[0] → rn_oldest_global=1, rn_newest_global=1
		//   gi=2: claims[1] → rn_oldest_global=1, rn_newest_global=1
		//   gi=100: claims[2](b1) rn_oldest=1, claims[3](b2) rn_oldest=2, claims[4](b3) rn_newest=1
		// claims_in_range [2,2]: claims[1](gi=2), claims[3](gi=100,rn_oldest_global=2,rn_newest=2)
		// compactable_claims: WHERE o.rn_oldest_global = 1 — claims[3] has rn_oldest_global=2 → excluded
		//   claims[1](gi=2) has rn_oldest_global=1 → included, joins with claims[1](rn_newest_global=1 in range)
		// claims_with_unset: no unset_claims → empty
		// Result: 1 compacted claim: gi=2 (claims[1])
		got, err := s.GetClaims(ctx, nil, 2, 2)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, big.NewInt(2), got[0].GlobalIndex)
	})
}

func TestGetClaimsByGlobalIndex_Compact(t *testing.T) {
	makeProof := func(h common.Hash) treetypes.Proof {
		var p treetypes.Proof
		p[0] = h
		return p
	}

	buildClaims := func() ([]claimsynctypes.Claim, claimsynctypes.UnsetClaim) {
		claims := []claimsynctypes.Claim{
			{ // claims[2]: block=1, gi=100 (oldest)
				BlockNum:            1,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x03"),
				GlobalIndex:         big.NewInt(100),
				Metadata:            []byte("original_metadata"),
				ProofLocalExitRoot:  makeProof(common.HexToHash("0x1a")),
				ProofRollupExitRoot: makeProof(common.HexToHash("0x1b")),
				MainnetExitRoot:     common.HexToHash("0x1c"),
				RollupExitRoot:      common.HexToHash("0x1d"),
				GlobalExitRoot:      common.HexToHash("0x1e"),
				Amount:              big.NewInt(3),
				Type:                claimsynctypes.ClaimEvent,
			},
			{ // claims[3]: block=2, gi=100 (middle)
				BlockNum:            2,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x04"),
				GlobalIndex:         big.NewInt(100),
				Metadata:            []byte("middle_metadata"),
				ProofLocalExitRoot:  makeProof(common.HexToHash("0x2a")),
				ProofRollupExitRoot: makeProof(common.HexToHash("0x2b")),
				MainnetExitRoot:     common.HexToHash("0x2c"),
				RollupExitRoot:      common.HexToHash("0x2d"),
				GlobalExitRoot:      common.HexToHash("0x2e"),
				Amount:              big.NewInt(4),
				Type:                claimsynctypes.ClaimEvent,
			},
			{ // claims[4]: block=3, gi=100 (newest)
				BlockNum:            3,
				BlockPos:            0,
				TxHash:              common.HexToHash("0x05"),
				GlobalIndex:         big.NewInt(100),
				Metadata:            []byte("newest_metadata"),
				ProofLocalExitRoot:  makeProof(common.HexToHash("0x3a")),
				ProofRollupExitRoot: makeProof(common.HexToHash("0x3b")),
				MainnetExitRoot:     common.HexToHash("0x3c"),
				RollupExitRoot:      common.HexToHash("0x3d"),
				GlobalExitRoot:      common.HexToHash("0x3e"),
				Amount:              big.NewInt(5),
				Type:                claimsynctypes.DetailedClaimEvent,
			},
		}

		unsetClaim := claimsynctypes.UnsetClaim{
			BlockNum:    5,
			BlockPos:    0,
			TxHash:      common.HexToHash("0xaa"),
			GlobalIndex: big.NewInt(100),
		}

		return claims, unsetClaim
	}

	t.Run("no unset_claim -> compacted", func(t *testing.T) {
		s, _ := newTestStorage(t)
		ctx := context.Background()
		claims, _ := buildClaims()

		insertedBlocks := map[uint64]bool{}
		for _, c := range claims {
			if !insertedBlocks[c.BlockNum] {
				require.NoError(t, s.InsertBlock(ctx, nil, c.BlockNum, common.Hash{}))
				insertedBlocks[c.BlockNum] = true
			}
			require.NoError(t, s.InsertClaim(ctx, nil, c))
		}

		got, err := s.GetClaimsByGlobalIndex(ctx, nil, big.NewInt(100))
		require.NoError(t, err)
		require.Len(t, got, 1)

		// Oldest metadata (block 1)
		require.Equal(t, claims[0].Metadata, got[0].Metadata)
		require.Equal(t, claims[0].BlockNum, got[0].BlockNum)
		// Newest proofs (block 3)
		require.Equal(t, claims[2].ProofLocalExitRoot, got[0].ProofLocalExitRoot)
		require.Equal(t, claims[2].MainnetExitRoot, got[0].MainnetExitRoot)
	})

	t.Run("with unset_claim -> uncompacted", func(t *testing.T) {
		s, _ := newTestStorage(t)
		ctx := context.Background()
		claims, unsetClaim := buildClaims()

		insertedBlocks := map[uint64]bool{}
		for _, c := range claims {
			if !insertedBlocks[c.BlockNum] {
				require.NoError(t, s.InsertBlock(ctx, nil, c.BlockNum, common.Hash{}))
				insertedBlocks[c.BlockNum] = true
			}
			require.NoError(t, s.InsertClaim(ctx, nil, c))
		}

		require.NoError(t, s.InsertBlock(ctx, nil, 5, common.Hash{}))
		require.NoError(t, s.InsertUnsetClaim(ctx, nil, unsetClaim))

		got, err := s.GetClaimsByGlobalIndex(ctx, nil, big.NewInt(100))
		require.NoError(t, err)
		require.Len(t, got, 3)
	})
}

func TestDatabaseQueryTimeout(t *testing.T) {
	lg := logger.GetDefaultLogger()
	dbPath := filepath.Join(t.TempDir(), "timeout_test.db")

	// Create storage with normal timeout for setup
	s, err := NewStandalone(lg, dbPath, "setup", 100*time.Millisecond)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, s.InsertBlock(ctx, nil, 1, common.Hash{}))

	// Create second storage pointing to same dbPath but with 1ns timeout
	s2, err := NewStandalone(lg, dbPath, "timeout_storage", time.Nanosecond)
	require.NoError(t, err)

	_, _, err = s2.GetLastProcessedBlock(ctx, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "context deadline exceeded")

	_, err = s2.GetClaims(ctx, nil, 1, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "context deadline exceeded")
}

func TestClaimColumnsSQL_ReflectionCheck(t *testing.T) {
	t.Parallel()

	claimType := reflect.TypeFor[claimsynctypes.Claim]()
	meddlerColumns := make([]string, 0, claimType.NumField())
	for i := range claimType.NumField() {
		field := claimType.Field(i)
		tag := field.Tag.Get("meddler")
		if tag == "" || tag == "-" {
			continue
		}
		// meddler tag format: "column_name" or "column_name,encoder"
		parts := strings.SplitN(tag, ",", 2)
		colName := parts[0]
		if colName == "" || colName == "-" {
			continue
		}
		meddlerColumns = append(meddlerColumns, colName)
	}

	// Normalize whitespace in claimColumnsSQL and split by comma
	normalized := regexp.MustCompile(`\s+`).ReplaceAllString(claimColumnsSQL, " ")
	normalized = strings.TrimSpace(normalized)
	rawCols := strings.Split(normalized, ",")
	sqlColumnSet := make(map[string]bool, len(rawCols))
	for _, col := range rawCols {
		col = strings.TrimSpace(col)
		if col != "" {
			sqlColumnSet[col] = true
		}
	}

	require.Equal(t, len(meddlerColumns), len(sqlColumnSet),
		"number of meddler-tagged fields (%d) != number of SQL columns (%d)",
		len(meddlerColumns), len(sqlColumnSet))

	for _, col := range meddlerColumns {
		require.True(t, sqlColumnSet[col],
			"meddler column %q not found in claimColumnsSQL", col)
	}
}

func TestGetBoundaryBlockForClaimType(t *testing.T) {
	testCases := []struct {
		name          string
		claimType     claimsynctypes.ClaimType
		claimsToInsert []struct {
			blockNum  uint64
			claimType claimsynctypes.ClaimType
		}
		expectedBlock uint64
		expectError   bool
		errorIs       error
	}{
		{
			name:          "no claims -> db.ErrNotFound",
			claimType:     claimsynctypes.DetailedClaimEvent,
			claimsToInsert: nil,
			expectError:   true,
			errorIs:       db.ErrNotFound,
		},
		{
			name:      "DetailedClaimEvent at blocks 1 and 6 -> returns 6",
			claimType: claimsynctypes.DetailedClaimEvent,
			claimsToInsert: []struct {
				blockNum  uint64
				claimType claimsynctypes.ClaimType
			}{
				{1, claimsynctypes.DetailedClaimEvent},
				{6, claimsynctypes.DetailedClaimEvent},
			},
			expectedBlock: 6,
		},
		{
			name:      "mixed types, DetailedClaimEvent at blocks 100 and 101 -> returns 101",
			claimType: claimsynctypes.DetailedClaimEvent,
			claimsToInsert: []struct {
				blockNum  uint64
				claimType claimsynctypes.ClaimType
			}{
				{50, claimsynctypes.ClaimEvent},
				{100, claimsynctypes.DetailedClaimEvent},
				{101, claimsynctypes.DetailedClaimEvent},
			},
			expectedBlock: 101,
		},
		{
			name:      "only ClaimEvent, ask DetailedClaimEvent -> db.ErrNotFound",
			claimType: claimsynctypes.DetailedClaimEvent,
			claimsToInsert: []struct {
				blockNum  uint64
				claimType claimsynctypes.ClaimType
			}{
				{10, claimsynctypes.ClaimEvent},
				{20, claimsynctypes.ClaimEvent},
			},
			expectError: true,
			errorIs:     db.ErrNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStorage(t)
			ctx := context.Background()

			insertedBlocks := map[uint64]bool{}
			for i, ci := range tc.claimsToInsert {
				if !insertedBlocks[ci.blockNum] {
					require.NoError(t, s.InsertBlock(ctx, nil, ci.blockNum, common.Hash{}))
					insertedBlocks[ci.blockNum] = true
				}
				claim := claimsynctypes.Claim{
					BlockNum:    ci.blockNum,
					BlockPos:    uint64(i),
					GlobalIndex: big.NewInt(int64(i + 1)),
					Amount:      big.NewInt(0),
					Type:        ci.claimType,
				}
				require.NoError(t, s.InsertClaim(ctx, nil, claim))
			}

			gotBlock, err := s.GetBoundaryBlockForClaimType(ctx, nil, tc.claimType)
			if tc.expectError {
				require.Error(t, err)
				if tc.errorIs != nil {
					require.ErrorIs(t, err, tc.errorIs)
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBlock, gotBlock)
			}
		})
	}
}

func TestGetClaimsByGER(t *testing.T) {
	s, _ := newTestStorage(t)
	ctx := context.Background()

	gerHash := common.HexToHash("0xaaaa1111")
	otherGER := common.HexToHash("0xbbbb2222")
	unknownGER := common.HexToHash("0xcccc3333")

	// Insert blocks
	require.NoError(t, s.InsertBlock(ctx, nil, 1, common.Hash{}))
	require.NoError(t, s.InsertBlock(ctx, nil, 2, common.Hash{}))
	require.NoError(t, s.InsertBlock(ctx, nil, 3, common.Hash{}))

	// detailedClaim: block=1, gi=100, ger=gerHash, type=DetailedClaimEvent
	detailedClaim := claimsynctypes.Claim{
		BlockNum:       1,
		BlockPos:       0,
		GlobalIndex:    big.NewInt(100),
		GlobalExitRoot: gerHash,
		Amount:         big.NewInt(0),
		Type:           claimsynctypes.DetailedClaimEvent,
	}
	require.NoError(t, s.InsertClaim(ctx, nil, detailedClaim))

	// claimEventSameGER: block=2, gi=200, ger=gerHash, type=ClaimEvent (should NOT be returned)
	claimEventSameGER := claimsynctypes.Claim{
		BlockNum:       2,
		BlockPos:       0,
		GlobalIndex:    big.NewInt(200),
		GlobalExitRoot: gerHash,
		Amount:         big.NewInt(0),
		Type:           claimsynctypes.ClaimEvent,
	}
	require.NoError(t, s.InsertClaim(ctx, nil, claimEventSameGER))

	// detailedOtherGER: block=3, gi=300, ger=otherGER, type=DetailedClaimEvent (should NOT be returned)
	detailedOtherGER := claimsynctypes.Claim{
		BlockNum:       3,
		BlockPos:       0,
		GlobalIndex:    big.NewInt(300),
		GlobalExitRoot: otherGER,
		Amount:         big.NewInt(0),
		Type:           claimsynctypes.DetailedClaimEvent,
	}
	require.NoError(t, s.InsertClaim(ctx, nil, detailedOtherGER))

	t.Run("returns only DetailedClaimEvent with matching GER", func(t *testing.T) {
		got, err := s.GetClaimsByGER(ctx, nil, gerHash)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.Equal(t, big.NewInt(100), got[0].GlobalIndex)
		require.Equal(t, claimsynctypes.DetailedClaimEvent, got[0].Type)
	})

	t.Run("returns empty for unknown GER", func(t *testing.T) {
		got, err := s.GetClaimsByGER(ctx, nil, unknownGER)
		require.NoError(t, err)
		require.Empty(t, got)
	})
}

func TestGetUnsetClaimsPaged(t *testing.T) {
	s, _ := newTestStorage(t)
	ctx := context.Background()

	unset := []claimsynctypes.UnsetClaim{
		{ // unset[0]: block=1, gi=100
			BlockNum:    1,
			BlockPos:    0,
			TxHash:      common.HexToHash("0x123"),
			GlobalIndex: big.NewInt(100),
			UnsetGlobalIndexHashChain: common.HexToHash("0xabc123"),
		},
		{ // unset[1]: block=2, gi=200
			BlockNum:    2,
			BlockPos:    0,
			TxHash:      common.HexToHash("0x456"),
			GlobalIndex: big.NewInt(200),
			UnsetGlobalIndexHashChain: common.HexToHash("0xdef456"),
		},
		{ // unset[2]: block=3, gi=100 (same gi as first)
			BlockNum:    3,
			BlockPos:    0,
			TxHash:      common.HexToHash("0x789"),
			GlobalIndex: big.NewInt(100),
			UnsetGlobalIndexHashChain: common.HexToHash("0x987654"),
		},
	}

	for _, u := range unset {
		require.NoError(t, s.InsertBlock(ctx, nil, u.BlockNum, common.Hash{}))
		require.NoError(t, s.InsertUnsetClaim(ctx, nil, u))
	}

	testCases := []struct {
		name          string
		pageNumber    uint32
		pageSize      uint32
		globalIndex   *big.Int
		expectedCount int
		expectedLen   int
		expectedGIs   []*big.Int
		expectError   bool
		errorContains string
	}{
		{
			name:          "all results",
			pageNumber:    1,
			pageSize:      10,
			globalIndex:   nil,
			expectedCount: 3,
			expectedLen:   3,
			expectedGIs:   []*big.Int{big.NewInt(100), big.NewInt(200), big.NewInt(100)},
		},
		{
			name:          "page 2 size 1",
			pageNumber:    2,
			pageSize:      1,
			globalIndex:   nil,
			expectedCount: 3,
			expectedLen:   1,
			expectedGIs:   []*big.Int{big.NewInt(200)},
		},
		{
			name:          "filter by gi=100",
			pageNumber:    1,
			pageSize:      10,
			globalIndex:   big.NewInt(100),
			expectedCount: 2,
			expectedLen:   2,
			expectedGIs:   []*big.Int{big.NewInt(100), big.NewInt(100)},
		},
		{
			name:          "non-existent gi",
			pageNumber:    1,
			pageSize:      10,
			globalIndex:   big.NewInt(9999),
			expectedCount: 0,
			expectedLen:   0,
		},
		{
			name:          "invalid page",
			pageNumber:    5,
			pageSize:      3,
			globalIndex:   nil,
			expectError:   true,
			errorContains: "invalid page number for given page size and total number of unset_claim",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, count, err := s.GetUnsetClaimsPaged(ctx, tc.pageNumber, tc.pageSize, tc.globalIndex)
			if tc.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errorContains)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.expectedCount, count)
			require.Len(t, got, tc.expectedLen)

			// Results are in DESC order (highest block_num first)
			for i, u := range got {
				if i < len(tc.expectedGIs) {
					require.Equal(t, tc.expectedGIs[i], u.GlobalIndex)
				}
			}
		})
	}
}

func TestGetSetClaimsPaged(t *testing.T) {
	s, _ := newTestStorage(t)
	ctx := context.Background()

	set := []claimsynctypes.SetClaim{
		{ // set[0]: block=1, gi=100
			BlockNum:    1,
			BlockPos:    0,
			TxHash:      common.HexToHash("0x111"),
			GlobalIndex: big.NewInt(100),
		},
		{ // set[1]: block=2, gi=200
			BlockNum:    2,
			BlockPos:    0,
			TxHash:      common.HexToHash("0x222"),
			GlobalIndex: big.NewInt(200),
		},
		{ // set[2]: block=3, gi=100
			BlockNum:    3,
			BlockPos:    0,
			TxHash:      common.HexToHash("0x333"),
			GlobalIndex: big.NewInt(100),
		},
		{ // set[3]: block=4, gi=300
			BlockNum:    4,
			BlockPos:    0,
			TxHash:      common.HexToHash("0x444"),
			GlobalIndex: big.NewInt(300),
		},
	}

	for _, sc := range set {
		require.NoError(t, s.InsertBlock(ctx, nil, sc.BlockNum, common.Hash{}))
		require.NoError(t, s.InsertSetClaim(ctx, nil, sc))
	}

	testCases := []struct {
		name          string
		pageNumber    uint32
		pageSize      uint32
		globalIndex   *big.Int
		expectedCount int
		expectedLen   int
		expectError   bool
		errorContains string
	}{
		{
			name:          "all results",
			pageNumber:    1,
			pageSize:      10,
			globalIndex:   nil,
			expectedCount: 4,
			expectedLen:   4,
		},
		{
			name:          "page 2 size 1",
			pageNumber:    2,
			pageSize:      1,
			globalIndex:   nil,
			expectedCount: 4,
			expectedLen:   1,
		},
		{
			name:          "filter by gi=100",
			pageNumber:    1,
			pageSize:      10,
			globalIndex:   big.NewInt(100),
			expectedCount: 2,
			expectedLen:   2,
		},
		{
			name:          "non-existent gi",
			pageNumber:    1,
			pageSize:      10,
			globalIndex:   big.NewInt(9999),
			expectedCount: 0,
			expectedLen:   0,
		},
		{
			name:          "invalid page",
			pageNumber:    5,
			pageSize:      4,
			globalIndex:   nil,
			expectError:   true,
			errorContains: "invalid page number for given page size and total number of set_claim",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, count, err := s.GetSetClaimsPaged(ctx, tc.pageNumber, tc.pageSize, tc.globalIndex)
			if tc.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errorContains)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.expectedCount, count)
			require.Len(t, got, tc.expectedLen)
		})
	}

	t.Run("all results descending order", func(t *testing.T) {
		t.Parallel()
		got, count, err := s.GetSetClaimsPaged(ctx, 1, 10, nil)
		require.NoError(t, err)
		require.Equal(t, 4, count)
		require.Len(t, got, 4)
		// DESC order: set[3](block4), set[2](block3), set[1](block2), set[0](block1)
		require.Equal(t, set[3].TxHash, got[0].TxHash)
		require.Equal(t, set[2].TxHash, got[1].TxHash)
		require.Equal(t, set[1].TxHash, got[2].TxHash)
		require.Equal(t, set[0].TxHash, got[3].TxHash)
	})
}

func TestGetClaimsPaged(t *testing.T) {
	s, _ := newTestStorage(t)
	ctx := context.Background()

	// 2^64 - 1
	uint64Max := new(big.Int).SetUint64(^uint64(0))
	// 18446744073709551617 = 2^64 + 1
	num1 := new(big.Int).Add(new(big.Int).SetUint64(^uint64(0)), big.NewInt(2))
	// 18446744073709551618 = 2^64 + 2
	num2 := new(big.Int).Add(new(big.Int).SetUint64(^uint64(0)), big.NewInt(3))
	// 2^256 - 1
	uint256Max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

	claims := []claimsynctypes.Claim{
		{ // claims[0]: block=1, gi=num2, originNetwork=1
			BlockNum:      1,
			BlockPos:      0,
			GlobalIndex:   new(big.Int).Set(num2),
			Amount:        big.NewInt(1),
			OriginNetwork: 1,
			Type:          claimsynctypes.ClaimEvent,
		},
		{ // claims[1]: block=2, gi=2, originNetwork=1
			BlockNum:      2,
			BlockPos:      0,
			GlobalIndex:   big.NewInt(2),
			Amount:        big.NewInt(1),
			OriginNetwork: 1,
			Type:          claimsynctypes.ClaimEvent,
		},
		{ // claims[2]: block=3, gi=uint64Max, originNetwork=2
			BlockNum:      3,
			BlockPos:      0,
			GlobalIndex:   new(big.Int).Set(uint64Max),
			Amount:        big.NewInt(1),
			OriginNetwork: 2,
			Type:          claimsynctypes.ClaimEvent,
		},
		{ // claims[3]: block=4, gi=num1, originNetwork=2
			BlockNum:      4,
			BlockPos:      0,
			GlobalIndex:   new(big.Int).Set(num1),
			Amount:        big.NewInt(1),
			OriginNetwork: 2,
			Type:          claimsynctypes.ClaimEvent,
		},
		{ // claims[4]: block=5, gi=5, originNetwork=3
			BlockNum:      5,
			BlockPos:      0,
			GlobalIndex:   big.NewInt(5),
			Amount:        big.NewInt(1),
			OriginNetwork: 3,
			Type:          claimsynctypes.ClaimEvent,
		},
		{ // claims[5]: block=6, gi=uint256Max, originNetwork=4
			BlockNum:      6,
			BlockPos:      0,
			GlobalIndex:   new(big.Int).Set(uint256Max),
			Amount:        big.NewInt(1),
			OriginNetwork: 4,
			Type:          claimsynctypes.ClaimEvent,
		},
	}

	// Insert blocks 1-10 and claims
	for i := uint64(1); i <= 10; i++ {
		require.NoError(t, s.InsertBlock(ctx, nil, i, common.Hash{}))
	}
	for _, c := range claims {
		require.NoError(t, s.InsertClaim(ctx, nil, c))
	}

	testCases := []struct {
		name          string
		pageNumber    uint32
		pageSize      uint32
		networkIDs    []uint32
		globalIndex   *big.Int
		expectedCount int
		expectedLen   int
		expectedGIs   []*big.Int
		expectError   bool
		errorContains string
	}{
		{
			name:          "page 2 size 1",
			pageNumber:    2,
			pageSize:      1,
			networkIDs:    nil,
			globalIndex:   nil,
			expectedCount: 6,
			expectedLen:   1,
			// DESC: claims[5](b6), claims[4](b5), ...
			// page 2 size 1 = offset 1 = claims[4]
			expectedGIs: []*big.Int{big.NewInt(5)},
		},
		{
			name:          "all on same page",
			pageNumber:    1,
			pageSize:      20,
			networkIDs:    nil,
			globalIndex:   nil,
			expectedCount: 6,
			expectedLen:   6,
			// DESC order: claims[5](b6), claims[4](b5), claims[3](b4), claims[2](b3), claims[1](b2), claims[0](b1)
			expectedGIs: []*big.Int{
				new(big.Int).Set(uint256Max),
				big.NewInt(5),
				new(big.Int).Set(num1),
				new(big.Int).Set(uint64Max),
				big.NewInt(2),
				new(big.Int).Set(num2),
			},
		},
		{
			name:          "page 2 size 3",
			pageNumber:    2,
			pageSize:      3,
			networkIDs:    nil,
			globalIndex:   nil,
			expectedCount: 6,
			expectedLen:   3,
			// offset=3: claims[2](b3), claims[1](b2), claims[0](b1)
			expectedGIs: []*big.Int{
				new(big.Int).Set(uint64Max),
				big.NewInt(2),
				new(big.Int).Set(num2),
			},
		},
		{
			name:          "invalid page",
			pageNumber:    4,
			pageSize:      3,
			networkIDs:    nil,
			globalIndex:   nil,
			expectError:   true,
			errorContains: "invalid page number for given page size and total number of claims",
		},
		{
			name:          "filter by networkIDs [1,3]",
			pageNumber:    1,
			pageSize:      3,
			networkIDs:    []uint32{1, 3},
			globalIndex:   nil,
			expectedCount: 3,
			expectedLen:   3,
			// claims[4](b5,net3), claims[1](b2,net1), claims[0](b1,net1)
			expectedGIs: []*big.Int{
				big.NewInt(5),
				big.NewInt(2),
				new(big.Int).Set(num2),
			},
		},
		{
			name:          "filter by gi=5",
			pageNumber:    1,
			pageSize:      3,
			networkIDs:    nil,
			globalIndex:   big.NewInt(5),
			expectedCount: 1,
			expectedLen:   1,
			expectedGIs:   []*big.Int{big.NewInt(5)},
		},
		{
			name:          "filter by networkIDs [2,3,4] and gi=uint64Max",
			pageNumber:    1,
			pageSize:      3,
			networkIDs:    []uint32{2, 3, 4},
			globalIndex:   new(big.Int).Set(uint64Max),
			expectedCount: 1,
			expectedLen:   1,
			expectedGIs:   []*big.Int{new(big.Int).Set(uint64Max)},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, count, err := s.GetClaimsPaged(ctx, tc.pageNumber, tc.pageSize, tc.networkIDs, tc.globalIndex)
			if tc.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errorContains)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.expectedCount, count)
			require.Len(t, got, tc.expectedLen)

			for i, gi := range tc.expectedGIs {
				require.Equal(t, 0, gi.Cmp(got[i].GlobalIndex),
					"index %d: expected gi=%s, got %s", i, gi.String(), got[i].GlobalIndex.String())
			}
		})
	}
}
