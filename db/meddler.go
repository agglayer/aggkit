package db

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"

	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	sqlite "github.com/mattn/go-sqlite3"
	"github.com/russross/meddler"
)

// init registers tags to be used to read/write from SQL DBs using meddler
func init() {
	meddler.Default = meddler.SQLite
	meddler.Register("bigint", BigIntMeddler{})
	meddler.Register("merkleproof", MerkleProofMeddler{})
	meddler.Register("hash", HashMeddler{})
	meddler.Register("address", AddressMeddler{})
}

func SQLiteErr(err error) (*sqlite.Error, bool) {
	sqliteErr := &sqlite.Error{}
	if ok := errors.As(err, sqliteErr); ok {
		return sqliteErr, true
	}
	if driverErr, ok := meddler.DriverErr(err); ok {
		return sqliteErr, errors.As(driverErr, sqliteErr)
	}
	return sqliteErr, false
}

// SliceToSlicePtrs converts any []Foo to []*Foo
func SliceToSlicePtrs(slice interface{}) interface{} {
	v := reflect.ValueOf(slice)
	vLen := v.Len()
	typ := v.Type().Elem()
	res := reflect.MakeSlice(reflect.SliceOf(reflect.PointerTo(typ)), vLen, vLen)
	for i := 0; i < vLen; i++ {
		res.Index(i).Set(v.Index(i).Addr())
	}
	return res.Interface()
}

// SlicePtrsToSlice converts any []*Foo to []Foo
func SlicePtrsToSlice(slice interface{}) interface{} {
	v := reflect.ValueOf(slice)
	vLen := v.Len()
	typ := v.Type().Elem().Elem()
	res := reflect.MakeSlice(reflect.SliceOf(typ), vLen, vLen)
	for i := 0; i < vLen; i++ {
		res.Index(i).Set(v.Index(i).Elem())
	}
	return res.Interface()
}

// RegisterMeddlerType registers a new meddler type with the given name
func RegisterMeddler(name string, meddlerType meddler.Meddler) {
	meddler.Register(name, meddlerType)
}

// BigIntMeddler encodes or decodes the field value to or from string
type BigIntMeddler struct{}

// PreRead is called before a Scan operation for fields that have the BigIntMeddler
func (b BigIntMeddler) PreRead(fieldAddr interface{}) (scanTarget interface{}, err error) {
	// give a pointer to a byte buffer to grab the raw data
	return new(string), nil
}

// PostRead is called after a Scan operation for fields that have the BigIntMeddler
func (b BigIntMeddler) PostRead(fieldPtr, scanTarget interface{}) error {
	ptr, ok := scanTarget.(*string)
	if !ok {
		return errors.New("scanTarget is not *string")
	}
	if ptr == nil {
		return fmt.Errorf("BigIntMeddler.PostRead: nil pointer")
	}
	field, ok := fieldPtr.(**big.Int)
	if !ok {
		return errors.New("fieldPtr is not *big.Int")
	}
	decimal := 10
	*field, ok = new(big.Int).SetString(*ptr, decimal)
	if !ok {
		return fmt.Errorf("big.Int.SetString failed on \"%v\"", *ptr)
	}
	return nil
}

// PreWrite is called before an Insert or Update operation for fields that have the BigIntMeddler
func (b BigIntMeddler) PreWrite(fieldPtr interface{}) (saveValue interface{}, err error) {
	field, ok := fieldPtr.(*big.Int)
	if !ok {
		return nil, errors.New("fieldPtr is not *big.Int")
	}

	return field.String(), nil
}

// MerkleProofMeddler encodes or decodes the field value to or from string
type MerkleProofMeddler struct{}

// PreRead is called before a Scan operation for fields that have the MerkleProofMeddler
func (b MerkleProofMeddler) PreRead(fieldAddr interface{}) (scanTarget interface{}, err error) {
	// give a pointer to a byte buffer to grab the raw data
	return new(string), nil
}

// PostRead is called after a Scan operation for fields that have the MerkleProofMeddler
func (b MerkleProofMeddler) PostRead(fieldPtr, scanTarget interface{}) error {
	ptr, ok := scanTarget.(*string)
	if !ok {
		return errors.New("scanTarget is not *string")
	}
	if ptr == nil {
		return errors.New("ProofMeddler.PostRead: nil pointer")
	}
	field, ok := fieldPtr.(*tree.Proof)
	if !ok {
		return errors.New("fieldPtr is not tree.Proof")
	}
	strHashes := strings.Split(*ptr, ",")
	if len(strHashes) != int(tree.DefaultHeight) {
		return fmt.Errorf("unexpected len of hashes: expected %d actual %d", tree.DefaultHeight, len(strHashes))
	}
	for i, strHash := range strHashes {
		field[i] = common.HexToHash(strHash)
	}
	return nil
}

// PreWrite is called before an Insert or Update operation for fields that have the MerkleProofMeddler
func (b MerkleProofMeddler) PreWrite(fieldPtr interface{}) (saveValue interface{}, err error) {
	field, ok := fieldPtr.(tree.Proof)
	if !ok {
		return nil, errors.New("fieldPtr is not tree.Proof")
	}
	var s string
	for _, f := range field {
		s += f.Hex() + ","
	}
	s = strings.TrimSuffix(s, ",")
	return s, nil
}

// HashMeddler encodes or decodes the field value to or from string
type HashMeddler struct{}

// PreRead is called before a Scan operation for fields that have the HashMeddler
func (m HashMeddler) PreRead(fieldAddr interface{}) (scanTarget interface{}, err error) {
	// give a pointer to a byte buffer to grab the raw data
	_, ok := fieldAddr.(**common.Hash)
	if ok {
		// This is because if not the rows.Scan fails 'converting NULL to string is unsupported'
		return new(*string), nil
	}
	return new(string), nil
}
func (m HashMeddler) postReadDoublePtr(fieldPtr, scanTarget interface{}) error {
	rawHashPtr, ok := scanTarget.(**string)
	if !ok {
		return errors.New("scanTarget is not **string")
	}
	// Handle the case where fieldPtr is a **common.Hash (nullable field)
	hashPtr, ok := fieldPtr.(**common.Hash)
	if ok {
		if rawHashPtr == nil || *rawHashPtr == nil {
			return nil
		}
		// If the string is empty, set the hash to nil
		if len(**rawHashPtr) == 0 {
			*hashPtr = nil
			// Otherwise, convert the string to a common.Hash and assign it
		} else {
			tmp := common.HexToHash(**rawHashPtr)
			*hashPtr = &tmp
		}
		return nil
	}
	return errors.New("fieldPtr is not **common.Hash")
}

// PostRead is called after a Scan operation for fields that have the HashMeddler
func (m HashMeddler) PostRead(fieldPtr, scanTarget interface{}) error {
	_, ok := scanTarget.(**string)
	if ok {
		return m.postReadDoublePtr(fieldPtr, scanTarget)
	}
	rawHashPtr, ok := scanTarget.(*string)
	if !ok {
		return errors.New("scanTarget is not *string")
	}

	// Handle the case where fieldPtr is a *common.Hash
	field, ok := fieldPtr.(*common.Hash)
	if ok {
		*field = common.HexToHash(*rawHashPtr)
		return nil
	}
	// If fieldPtr is neither a *common.Hash, return an error
	return errors.New("fieldPtr is not *common.Hash")
}

// PreWrite is called before an Insert or Update operation for fields that have the HashMeddler
func (m HashMeddler) PreWrite(fieldPtr interface{}) (saveValue interface{}, err error) {
	field, ok := fieldPtr.(common.Hash)
	if !ok {
		hashPtr, ok := fieldPtr.(*common.Hash)
		if !ok {
			return nil, errors.New("fieldPtr is not common.Hash")
		}
		if hashPtr == nil {
			return []byte{}, nil
		}
		return hashPtr.Hex(), nil
	}
	return field.Hex(), nil
}

// AddressMeddler encodes or decodes the field value to or from string
type AddressMeddler struct{}

// PreRead is called before a Scan operation for fields that have the AddressMeddler
func (b AddressMeddler) PreRead(fieldAddr interface{}) (scanTarget interface{}, err error) {
	// give a pointer to a byte buffer to grab the raw data
	return new(string), nil
}

// PostRead is called after a Scan operation for fields that have the AddressMeddler
func (b AddressMeddler) PostRead(fieldPtr, scanTarget interface{}) error {
	ptr, ok := scanTarget.(*string)
	if !ok {
		return errors.New("scanTarget is not *string")
	}
	if ptr == nil {
		return errors.New("AddressMeddler.PostRead: nil pointer")
	}
	field, ok := fieldPtr.(*common.Address)
	if !ok {
		return errors.New("fieldPtr is not common.Address")
	}
	*field = common.HexToAddress(*ptr)
	return nil
}

// PreWrite is called before an Insert or Update operation for fields that have the AddressMeddler
func (b AddressMeddler) PreWrite(fieldPtr interface{}) (saveValue interface{}, err error) {
	field, ok := fieldPtr.(common.Address)
	if !ok {
		return nil, errors.New("fieldPtr is not common.Address")
	}
	return field.Hex(), nil
}
