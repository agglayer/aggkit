package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/agglayer/aggkit/db"
	dbtypes "github.com/agglayer/aggkit/db/types"
	"github.com/russross/meddler"
)

const (
	// orderByBlockDesc is the default order by clause for block-based queries
	orderByBlockDesc = "block_num DESC, block_pos DESC"
	// unsetClaimTableName is the name of the table that stores unset claim events
	unsetClaimTableName = "unset_claim"

	// setClaimTableName is the name of the table that stores set claim events
	setClaimTableName = "set_claim"
)

var (
	// tableNameRegex is the regex pattern to validate table names
	tableNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
)

func (p *claimStorage) GetSetClaimsPaged(
	ctx context.Context, pageNumber, pageSize uint32,
	globalIndex *big.Int,
) ([]*claimsynctypes.SetClaim, int, error) {
	whereClause := buildGlobalIndexFilterClause(globalIndex)
	setClaimsCount, err := p.GetTotalNumberOfRecords(ctx, setClaimTableName, whereClause)
	if err != nil {
		return nil, 0, err
	}

	if setClaimsCount == 0 {
		return []*claimsynctypes.SetClaim{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, setClaimsCount, setClaimTableName)
	if err != nil {
		return nil, 0, err
	}

	rows, err := p.queryPaged(ctx, p.database, offset, pageSize, setClaimTableName, orderByBlockDesc, whereClause)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no set claims were found for provided parameters (pageNumber=%d, pageSize=%d)",
				pageNumber, pageSize)
			return nil, setClaimsCount, nil
		}
		p.log.Errorf("GetSetClaimsPaged: queryPaged failed for pageNumber=%d, pageSize=%d: %v", pageNumber, pageSize, err)
		return nil, 0, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	setClaims := []*claimsynctypes.SetClaim{}
	if err = meddler.ScanAll(rows, &setClaims); err != nil {
		p.log.Errorf("GetSetClaimsPaged: meddler.ScanAll failed for pageNumber=%d, pageSize=%d: %v",
			pageNumber, pageSize, err)
		return nil, 0, err
	}

	return setClaims, setClaimsCount, nil
}

// GetUnsetClaimsPaged returns a paginated list of unset claims
//
//nolint:dupl
func (p *claimStorage) GetUnsetClaimsPaged(
	ctx context.Context, pageNumber, pageSize uint32,
	globalIndex *big.Int,
) ([]*claimsynctypes.UnsetClaim, int, error) {
	whereClause := buildGlobalIndexFilterClause(globalIndex)
	unclaimsCount, err := p.GetTotalNumberOfRecords(ctx, unsetClaimTableName, whereClause)
	if err != nil {
		return nil, 0, err
	}

	if unclaimsCount == 0 {
		return []*claimsynctypes.UnsetClaim{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, unclaimsCount, unsetClaimTableName)
	if err != nil {
		return nil, 0, err
	}

	rows, err := p.queryPaged(ctx, p.database, offset, pageSize, unsetClaimTableName, orderByBlockDesc, whereClause)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			p.log.Debugf("no unset claims were found for provided parameters (pageNumber=%d, pageSize=%d)",
				pageNumber, pageSize)
			return nil, unclaimsCount, nil
		}
		p.log.Errorf("GetUnsetClaimsPaged: queryPaged failed for pageNumber=%d, pageSize=%d: %v", pageNumber, pageSize, err)
		return nil, 0, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	unsetClaims := []*claimsynctypes.UnsetClaim{}
	if err = meddler.ScanAll(rows, &unsetClaims); err != nil {
		p.log.Errorf("GetUnsetClaimsPaged: meddler.ScanAll failed for pageNumber=%d, pageSize=%d: %v",
			pageNumber, pageSize, err)
		return nil, 0, err
	}

	return unsetClaims, unclaimsCount, nil
}

func (p *claimStorage) GetClaimsPaged(
	ctx context.Context, pageNumber, pageSize uint32,
	networkIDs []uint32, globalIndex *big.Int,
) ([]*claimsynctypes.Claim, int, error) {
	whereClause := p.buildClaimsFilterClause(networkIDs, globalIndex)
	claimsCount, err := p.getCompactedClaimsCount(ctx, whereClause)
	if err != nil {
		return nil, 0, err
	}

	if claimsCount == 0 {
		return []*claimsynctypes.Claim{}, 0, nil
	}

	offset, err := p.calculateOffset(pageNumber, pageSize, claimsCount, "claims")
	if err != nil {
		return nil, 0, err
	}

	// Create a context with database timeout
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	// Pagination query with compaction logic implementing three cases:
	// Case 1: If unset_claim exists for a global_index, return all claims on page uncompacted
	// Case 2: If no unset_claim exists and globally oldest is on page, return compacted claim
	// Case 3: If globally oldest is outside page and no unset_claim exists, exclude from results
	//
	// This query:
	// - Gets claims for the requested page (DESC order: newest first)
	// - Ranks all claims globally by global_index to find oldest and newest
	// - For claims with unset_claim: returns all instances on the page uncompacted
	// - For claims without unset_claim: only returns compacted version if newest is on page
	//nolint:gosec
	query := fmt.Sprintf(`
		WITH page_claims AS (
			SELECT *
			FROM claim
			%s
			ORDER BY block_num DESC, block_pos DESC
			LIMIT $1 OFFSET $2
		),
		all_claims_ranked AS (
			SELECT
				*,
				ROW_NUMBER() OVER (PARTITION BY global_index ORDER BY block_num ASC, block_pos ASC) AS rn_oldest_global,
				ROW_NUMBER() OVER (PARTITION BY global_index ORDER BY block_num DESC, block_pos DESC) AS rn_newest_global
			FROM claim
			%s
		),
		claims_with_unset_on_page AS (
			-- Case 1: Return all claims on page if unset_claim exists (no compaction)
			SELECT
				pc.%s
			FROM page_claims pc
			WHERE EXISTS (
				SELECT 1 FROM unset_claim uc
				WHERE uc.global_index = pc.global_index
			)
		),
		newest_on_page AS (
			SELECT DISTINCT pc.global_index
			FROM page_claims pc
			JOIN all_claims_ranked acr ON pc.global_index = acr.global_index AND acr.rn_newest_global = 1
			WHERE pc.block_num = acr.block_num AND pc.block_pos = acr.block_pos
			AND NOT EXISTS (
				SELECT 1 FROM unset_claim uc
				WHERE uc.global_index = pc.global_index
			)
		),
		compactable_claims AS (
			-- Case 2 & 3: Handle claims without unset_claim
			SELECT
			%s
			FROM all_claims_ranked o
			JOIN all_claims_ranked n ON o.global_index = n.global_index AND n.rn_newest_global = 1
			WHERE o.rn_oldest_global = 1  -- Globally oldest claim
			AND o.global_index IN (SELECT global_index FROM newest_on_page)
		)
		SELECT * FROM claims_with_unset_on_page
		UNION ALL
		SELECT * FROM compactable_claims
		ORDER BY block_num DESC, block_pos DESC;
	`, whereClause, whereClause, claimColumnsSQL, compactedClaimsSelectSQL)

	rows, err := p.database.QueryContext(dbCtx, query, pageSize, offset)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			p.log.Debugf("no claims were found for provided parameters (pageNumber=%d, pageSize=%d)",
				pageNumber, pageSize)
			return nil, claimsCount, nil
		}
		p.log.Errorf("GetClaimsPaged: queryPaged failed for pageNumber=%d, pageSize=%d: %v", pageNumber, pageSize, err)
		return nil, 0, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			p.log.Errorf("error closing rows: %v", cerr)
		}
	}()

	claims := []*claimsynctypes.Claim{}
	if err = meddler.ScanAll(rows, &claims); err != nil {
		p.log.Errorf("GetClaimsPaged: meddler.ScanAll failed for pageNumber=%d, pageSize=%d: %v", pageNumber, pageSize, err)
		return nil, 0, err
	}

	return claims, claimsCount, nil
}

// buildGlobalIndexFilterClause builds a WHERE clause for filtering by global_index
func buildGlobalIndexFilterClause(globalIndex *big.Int) string {
	if globalIndex != nil {
		return " WHERE " + fmt.Sprintf("global_index = '%s'", globalIndex.String())
	}

	return ""
}

// buildClaimsFilterClause builds the WHERE clause for the claims table
// based on the provided networkIDs and globalIndex
func (p *claimStorage) buildClaimsFilterClause(networkIDs []uint32, globalIndex *big.Int) string {
	const clauseCapacity = 2
	clauses := make([]string, 0, clauseCapacity)
	if len(networkIDs) > 0 {
		clauses = append(clauses, buildNetworkIDsFilter(networkIDs, "origin_network"))
	}

	if globalIndex != nil {
		clauses = append(clauses,
			fmt.Sprintf("global_index = '%s'", globalIndex.String()),
		)
	}

	if len(clauses) > 0 {
		return " WHERE " + strings.Join(clauses, " AND ")
	}
	return ""
}

// getCompactedClaimsCount returns the count of claims with compaction logic applied.
// - If unset_claim exists for a global_index, count all claims with that global_index
// - If no unset_claim exists, count only one per global_index (compacted)
// The count represents the total across all pages, matching what would be returned
// if all pages were queried.
func (p *claimStorage) getCompactedClaimsCount(ctx context.Context, whereClause string) (int, error) {
	// Create a context with database timeout
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	// Count query with compaction logic matching GetClaimsPaged:
	// 1. Count all claims with unset_claim (no compaction, all returned)
	// 2. Count distinct global_index for claims without unset_claim (compacted, one per global_index)
	//nolint:gosec
	query := fmt.Sprintf(`
		WITH filtered_claims AS (
			SELECT * FROM claim %s
		)
		SELECT
			(SELECT COUNT(*) FROM filtered_claims
			 WHERE EXISTS (
				SELECT 1 FROM unset_claim uc
				WHERE uc.global_index = filtered_claims.global_index
			 )) +
			(SELECT COUNT(DISTINCT global_index) FROM filtered_claims
			 WHERE NOT EXISTS (
				SELECT 1 FROM unset_claim uc
				WHERE uc.global_index = filtered_claims.global_index
			 )) AS total_count;
	`, whereClause)

	count := 0
	err := p.database.QueryRowContext(dbCtx, query).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

func (p *claimStorage) calculateOffset(pageNumber, pageSize uint32,
	recordsCount int, tableName string) (uint32, error) {
	offset := (pageNumber - 1) * pageSize
	if offset >= uint32(recordsCount) {
		msg := fmt.Sprintf("invalid page number for given page size and total number of %s (page=%d, size=%d, total=%d)",
			tableName, pageNumber, pageSize, recordsCount)
		p.log.Debugf(msg)
		return 0, errors.New(msg)
	}
	return offset, nil
}

// GetTotalNumberOfRecords returns the total number of records in the given table
func (p *claimStorage) GetTotalNumberOfRecords(ctx context.Context, tableName, whereClause string) (int, error) {
	if !tableNameRegex.MatchString(tableName) {
		return 0, fmt.Errorf("invalid table name '%s' provided", tableName)
	}

	// Create a context with database timeout
	dbCtx, cancel := p.withDatabaseTimeout(ctx)
	defer cancel()

	count := 0
	err := p.database.QueryRowContext(dbCtx, fmt.Sprintf(
		`SELECT COUNT(*) AS count FROM %s%s;`, tableName, whereClause,
	)).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// queryPaged returns a paged result from the given table with context support
func (p *claimStorage) queryPaged(ctx context.Context, tx dbtypes.Querier,
	offset, pageSize uint32,
	table, orderByClause, whereClause string,
) (*sql.Rows, error) {
	// Create a context with database timeout
	dbCtx, _ := p.withDatabaseTimeout(ctx)
	rows, err := tx.QueryContext(dbCtx, fmt.Sprintf(`
		SELECT *
		FROM %s
		%s
		ORDER BY %s
		LIMIT $1 OFFSET $2;
	`, table, whereClause, orderByClause), pageSize, offset)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return rows, nil
}
