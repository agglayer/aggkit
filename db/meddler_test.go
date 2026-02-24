package db

import (
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"testing"

	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	sqlite "github.com/mattn/go-sqlite3"
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
}

func TestMeddlerHashpostReadDoublePtrBadParams(t *testing.T) {
	h := HashMeddler{}
	err := h.postReadDoublePtr(nil, nil)
	require.Error(t, err)
}

func TestSQLiteErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		err          error
		expectSQLite bool
		expectedCode sqlite.ErrNo
	}{
		{
			name:         "direct sqlite error",
			err:          sqlite.Error{Code: sqlite.ErrConstraint},
			expectSQLite: true,
			expectedCode: sqlite.ErrConstraint,
		},
		{
			name:         "non-sqlite error",
			err:          errors.New("generic error"),
			expectSQLite: false,
		},
		{
			name:         "nil error",
			err:          nil,
			expectSQLite: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sqliteErr, ok := SQLiteErr(tt.err)
			require.Equal(t, tt.expectSQLite, ok)
			if tt.expectSQLite {
				require.NotNil(t, sqliteErr)
				if tt.expectedCode != 0 {
					require.Equal(t, tt.expectedCode, sqliteErr.Code)
				}
			}
		})
	}
}

func TestSliceToSlicePtrs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
	}{
		{
			name:     "empty int slice",
			input:    []int{},
			expected: []*int{},
		},
		{
			name:     "int slice with values",
			input:    []int{1, 2, 3},
			expected: []*int{},
		},
		{
			name:     "string slice",
			input:    []string{"a", "b", "c"},
			expected: []*string{},
		},
		{
			name:     "empty string slice",
			input:    []string{},
			expected: []*string{},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := SliceToSlicePtrs(tt.input)
			require.NotNil(t, result)

			// Verify the result type matches expected pointer slice type
			switch v := tt.input.(type) {
			case []int:
				ptrs, ok := result.([]*int)
				require.True(t, ok, "result should be []*int")
				require.Equal(t, len(v), len(ptrs))
				for i, val := range v {
					require.Equal(t, val, *ptrs[i])
				}
			case []string:
				ptrs, ok := result.([]*string)
				require.True(t, ok, "result should be []*string")
				require.Equal(t, len(v), len(ptrs))
				for i, val := range v {
					require.Equal(t, val, *ptrs[i])
				}
			}
		})
	}
}

func TestSlicePtrsToSlice(t *testing.T) {
	t.Parallel()

	// Create test data
	i1, i2, i3 := 1, 2, 3
	s1, s2, s3 := "a", "b", "c"

	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
	}{
		{
			name:     "empty int pointer slice",
			input:    []*int{},
			expected: []int{},
		},
		{
			name:     "int pointer slice with values",
			input:    []*int{&i1, &i2, &i3},
			expected: []int{1, 2, 3},
		},
		{
			name:     "string pointer slice",
			input:    []*string{&s1, &s2, &s3},
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "empty string pointer slice",
			input:    []*string{},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := SlicePtrsToSlice(tt.input)
			require.NotNil(t, result)

			// Verify the result matches expected
			switch expected := tt.expected.(type) {
			case []int:
				actual, ok := result.([]int)
				require.True(t, ok, "result should be []int")
				require.Equal(t, expected, actual)
			case []string:
				actual, ok := result.([]string)
				require.True(t, ok, "result should be []string")
				require.Equal(t, expected, actual)
			}
		})
	}
}

func TestBigIntMeddler_PreRead(t *testing.T) {
	t.Parallel()

	b := BigIntMeddler{}
	scanTarget, err := b.PreRead(nil)
	require.NoError(t, err)
	require.NotNil(t, scanTarget)

	_, ok := scanTarget.(*string)
	require.True(t, ok, "scanTarget should be *string")
}

func TestBigIntMeddler_PostRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		scanTarget  interface{}
		fieldPtr    interface{}
		expected    *big.Int
		expectError bool
		errorMsg    string
	}{
		{
			name:       "valid big int string",
			scanTarget: func() *string { s := "12345"; return &s }(),
			fieldPtr:   new(*big.Int),
			expected:   big.NewInt(12345),
		},
		{
			name:       "zero value",
			scanTarget: func() *string { s := "0"; return &s }(),
			fieldPtr:   new(*big.Int),
			expected:   big.NewInt(0),
		},
		{
			name:       "large number",
			scanTarget: func() *string { s := "999999999999999999999999"; return &s }(),
			fieldPtr:   new(*big.Int),
			expected:   func() *big.Int { i, _ := new(big.Int).SetString("999999999999999999999999", 10); return i }(),
		},
		{
			name:        "invalid scan target type",
			scanTarget:  "not a pointer",
			fieldPtr:    new(*big.Int),
			expectError: true,
			errorMsg:    "scanTarget is not *string",
		},
		{
			name:        "nil scan target",
			scanTarget:  (*string)(nil),
			fieldPtr:    new(*big.Int),
			expectError: true,
			errorMsg:    "nil pointer",
		},
		{
			name:        "invalid field pointer type",
			scanTarget:  func() *string { s := "123"; return &s }(),
			fieldPtr:    new(string),
			expectError: true,
			errorMsg:    "fieldPtr is not *big.Int",
		},
		{
			name:        "invalid number string",
			scanTarget:  func() *string { s := "not a number"; return &s }(),
			fieldPtr:    new(*big.Int),
			expectError: true,
			errorMsg:    "SetString failed",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := BigIntMeddler{}
			err := b.PostRead(tt.fieldPtr, tt.scanTarget)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
				field, ok := tt.fieldPtr.(**big.Int)
				require.True(t, ok)
				require.Equal(t, tt.expected.String(), (*field).String())
			}
		})
	}
}

func TestBigIntMeddler_PreWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fieldPtr    interface{}
		expected    interface{}
		expectError bool
	}{
		{
			name:     "valid big int",
			fieldPtr: big.NewInt(12345),
			expected: "12345",
		},
		{
			name:     "zero value",
			fieldPtr: big.NewInt(0),
			expected: "0",
		},
		{
			name:     "negative value",
			fieldPtr: big.NewInt(-999),
			expected: "-999",
		},
		{
			name:     "large number",
			fieldPtr: func() *big.Int { i, _ := new(big.Int).SetString("999999999999999999999999", 10); return i }(),
			expected: "999999999999999999999999",
		},
		{
			name:        "invalid type",
			fieldPtr:    "not a big int",
			expectError: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := BigIntMeddler{}
			saveValue, err := b.PreWrite(tt.fieldPtr)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), "fieldPtr is not *big.Int")
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, saveValue)
			}
		})
	}
}

func TestMerkleProofMeddler_PreRead(t *testing.T) {
	t.Parallel()

	m := MerkleProofMeddler{}
	scanTarget, err := m.PreRead(nil)
	require.NoError(t, err)
	require.NotNil(t, scanTarget)

	_, ok := scanTarget.(*string)
	require.True(t, ok, "scanTarget should be *string")
}

func TestMerkleProofMeddler_PostRead(t *testing.T) {
	t.Parallel()

	// Create a valid proof string with 32 hashes
	validHashes := make([]string, tree.DefaultHeight)
	for i := range validHashes {
		validHashes[i] = fmt.Sprintf("0x%064d", i)
	}
	validProofString := func() *string {
		s := "" + validHashes[0]
		for i := 1; i < len(validHashes); i++ {
			s += "," + validHashes[i]
		}
		return &s
	}()

	tests := []struct {
		name        string
		scanTarget  interface{}
		fieldPtr    interface{}
		expectError bool
		errorMsg    string
	}{
		{
			name:       "valid proof",
			scanTarget: validProofString,
			fieldPtr:   new(tree.Proof),
		},
		{
			name:        "invalid scan target type",
			scanTarget:  "not a pointer",
			fieldPtr:    new(tree.Proof),
			expectError: true,
			errorMsg:    "scanTarget is not *string",
		},
		{
			name:        "nil scan target",
			scanTarget:  (*string)(nil),
			fieldPtr:    new(tree.Proof),
			expectError: true,
			errorMsg:    "nil pointer",
		},
		{
			name:        "invalid field pointer type",
			scanTarget:  validProofString,
			fieldPtr:    new(string),
			expectError: true,
			errorMsg:    "fieldPtr is not tree.Proof",
		},
		{
			name:        "wrong number of hashes",
			scanTarget:  func() *string { s := "0x123,0x456"; return &s }(),
			fieldPtr:    new(tree.Proof),
			expectError: true,
			errorMsg:    "unexpected len of hashes",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := MerkleProofMeddler{}
			err := m.PostRead(tt.fieldPtr, tt.scanTarget)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
				field, ok := tt.fieldPtr.(*tree.Proof)
				require.True(t, ok)
				require.NotNil(t, field)
			}
		})
	}
}

func TestMerkleProofMeddler_PreWrite(t *testing.T) {
	t.Parallel()

	// Create a valid proof
	var proof tree.Proof
	for i := range proof {
		proof[i] = common.HexToHash(fmt.Sprintf("0x%064d", i))
	}

	tests := []struct {
		name        string
		fieldPtr    interface{}
		expectError bool
	}{
		{
			name:     "valid proof",
			fieldPtr: proof,
		},
		{
			name:        "invalid type",
			fieldPtr:    "not a proof",
			expectError: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := MerkleProofMeddler{}
			saveValue, err := m.PreWrite(tt.fieldPtr)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), "fieldPtr is not tree.Proof")
			} else {
				require.NoError(t, err)
				require.NotNil(t, saveValue)
				str, ok := saveValue.(string)
				require.True(t, ok)
				require.NotEmpty(t, str)
			}
		})
	}
}

func TestHashMeddler_PreRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		fieldAddr       interface{}
		expectedType    string
		expectedIsNil   bool
		expectDoublePtr bool
	}{
		{
			name:            "nullable hash field",
			fieldAddr:       new(*common.Hash),
			expectedType:    "**string",
			expectDoublePtr: true,
		},
		{
			name:         "non-nullable hash field",
			fieldAddr:    new(common.Hash),
			expectedType: "*string",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := HashMeddler{}
			scanTarget, err := h.PreRead(tt.fieldAddr)
			require.NoError(t, err)
			require.NotNil(t, scanTarget)

			if tt.expectDoublePtr {
				_, ok := scanTarget.(**string)
				require.True(t, ok, "scanTarget should be **string")
			} else {
				_, ok := scanTarget.(*string)
				require.True(t, ok, "scanTarget should be *string")
			}
		})
	}
}

func TestHashMeddler_PostRead(t *testing.T) {
	t.Parallel()

	hex := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	hash := common.HexToHash(hex)

	tests := []struct {
		name        string
		scanTarget  interface{}
		fieldPtr    interface{}
		expected    *common.Hash
		expectError bool
		errorMsg    string
	}{
		{
			name:       "valid hash",
			scanTarget: func() *string { s := hex; return &s }(),
			fieldPtr:   new(common.Hash),
			expected:   &hash,
		},
		{
			name:       "nullable hash with value",
			scanTarget: func() **string { s := hex; ps := &s; return &ps }(),
			fieldPtr:   new(*common.Hash),
			expected:   &hash,
		},
		{
			name:       "nullable hash with nil",
			scanTarget: func() **string { var ps *string; return &ps }(),
			fieldPtr:   new(*common.Hash),
			expected:   nil,
		},
		{
			name:       "nullable hash with empty string",
			scanTarget: func() **string { s := ""; ps := &s; return &ps }(),
			fieldPtr:   new(*common.Hash),
			expected:   nil,
		},
		{
			name:        "invalid scan target",
			scanTarget:  123,
			fieldPtr:    new(common.Hash),
			expectError: true,
			errorMsg:    "scanTarget is not *string",
		},
		{
			name:        "invalid field pointer",
			scanTarget:  func() *string { s := hex; return &s }(),
			fieldPtr:    new(string),
			expectError: true,
			errorMsg:    "fieldPtr is not *common.Hash",
		},
		{
			name:        "double ptr invalid field pointer",
			scanTarget:  func() **string { s := hex; ps := &s; return &ps }(),
			fieldPtr:    new(string),
			expectError: true,
			errorMsg:    "fieldPtr is not **common.Hash",
		},
	}

	//nolint:dupl // Similar test structure by design for different types
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := HashMeddler{}
			err := h.PostRead(tt.fieldPtr, tt.scanTarget)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
				if hashPtrPtr, ok := tt.fieldPtr.(**common.Hash); ok {
					if tt.expected == nil {
						require.Nil(t, *hashPtrPtr)
					} else {
						require.NotNil(t, *hashPtrPtr)
						require.Equal(t, *tt.expected, **hashPtrPtr)
					}
				} else if hashPtr, ok := tt.fieldPtr.(*common.Hash); ok {
					require.Equal(t, *tt.expected, *hashPtr)
				}
			}
		})
	}
}

func TestAddressMeddler_PreRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		fieldAddr       interface{}
		expectDoublePtr bool
	}{
		{
			name:            "nullable address field",
			fieldAddr:       new(*common.Address),
			expectDoublePtr: true,
		},
		{
			name:      "non-nullable address field",
			fieldAddr: new(common.Address),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := AddressMeddler{}
			scanTarget, err := a.PreRead(tt.fieldAddr)
			require.NoError(t, err)
			require.NotNil(t, scanTarget)

			if tt.expectDoublePtr {
				_, ok := scanTarget.(**string)
				require.True(t, ok, "scanTarget should be **string")
			} else {
				_, ok := scanTarget.(*string)
				require.True(t, ok, "scanTarget should be *string")
			}
		})
	}
}

func TestAddressMeddler_PostRead(t *testing.T) {
	t.Parallel()

	addrHex := "0x1234567890123456789012345678901234567890"
	addr := common.HexToAddress(addrHex)

	tests := []struct {
		name        string
		scanTarget  interface{}
		fieldPtr    interface{}
		expected    *common.Address
		expectError bool
		errorMsg    string
	}{
		{
			name:       "valid address",
			scanTarget: func() *string { s := addrHex; return &s }(),
			fieldPtr:   new(common.Address),
			expected:   &addr,
		},
		{
			name:       "nullable address with value",
			scanTarget: func() **string { s := addrHex; ps := &s; return &ps }(),
			fieldPtr:   new(*common.Address),
			expected:   &addr,
		},
		{
			name:       "nullable address with nil",
			scanTarget: func() **string { var ps *string; return &ps }(),
			fieldPtr:   new(*common.Address),
			expected:   nil,
		},
		{
			name:       "nullable address with empty string",
			scanTarget: func() **string { s := ""; ps := &s; return &ps }(),
			fieldPtr:   new(*common.Address),
			expected:   nil,
		},
		{
			name:        "invalid scan target type",
			scanTarget:  123,
			fieldPtr:    new(common.Address),
			expectError: true,
			errorMsg:    "scanTarget is not *string or **string",
		},
		{
			name:        "nil scan target pointer",
			scanTarget:  (*string)(nil),
			fieldPtr:    new(common.Address),
			expectError: true,
			errorMsg:    "nil pointer",
		},
		{
			name:        "invalid field pointer type",
			scanTarget:  func() *string { s := addrHex; return &s }(),
			fieldPtr:    new(string),
			expectError: true,
			errorMsg:    "fieldPtr is not *common.Address",
		},
		{
			name:        "double ptr invalid field pointer",
			scanTarget:  func() **string { s := addrHex; ps := &s; return &ps }(),
			fieldPtr:    new(string),
			expectError: true,
			errorMsg:    "fieldPtr is not **common.Address",
		},
	}

	//nolint:dupl // Similar test structure by design for different types
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := AddressMeddler{}
			err := a.PostRead(tt.fieldPtr, tt.scanTarget)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
				if addrPtrPtr, ok := tt.fieldPtr.(**common.Address); ok {
					if tt.expected == nil {
						require.Nil(t, *addrPtrPtr)
					} else {
						require.NotNil(t, *addrPtrPtr)
						require.Equal(t, *tt.expected, **addrPtrPtr)
					}
				} else if addrPtr, ok := tt.fieldPtr.(*common.Address); ok {
					require.Equal(t, *tt.expected, *addrPtr)
				}
			}
		})
	}
}

func TestAddressMeddler_PreWrite(t *testing.T) {
	t.Parallel()

	addrHex := "0x1234567890123456789012345678901234567890"
	addr := common.HexToAddress(addrHex)

	tests := []struct {
		name        string
		fieldPtr    interface{}
		expected    interface{}
		expectError bool
	}{
		{
			name:     "valid address",
			fieldPtr: addr,
			expected: addr.Hex(),
		},
		{
			name:     "valid address pointer",
			fieldPtr: &addr,
			expected: addr.Hex(),
		},
		{
			name:     "nil address pointer",
			fieldPtr: (*common.Address)(nil),
			expected: nil,
		},
		{
			name:        "invalid type",
			fieldPtr:    "not an address",
			expectError: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := AddressMeddler{}
			saveValue, err := a.PreWrite(tt.fieldPtr)

			if tt.expectError {
				require.Error(t, err)
				require.Contains(t, err.Error(), "fieldPtr is not common.Address")
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, saveValue)
			}
		})
	}
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
