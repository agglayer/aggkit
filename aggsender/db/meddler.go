package db

import (
	"encoding/json"
	"errors"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/db"
)

func init() {
	// this is done like this to make sure that init() function in db package is called
	// before this init() function
	db.RegisterMeddler("aggchainproof", &AggchainProofMeddler{})
}

// AggchainProofMeddler is a meddler.Meddler implementation for the AggchainProof type.
type AggchainProofMeddler struct{}

// PreRead prepares the field for reading from the database.
func (m *AggchainProofMeddler) PreRead(fieldAddr interface{}) (scanTarget interface{}, err error) {
	return &[]byte{}, nil
}

// PostRead decodes the data from the database into the field.
func (m *AggchainProofMeddler) PostRead(fieldAddr interface{}, scanTarget interface{}) error {
	if fieldAddr == nil || scanTarget == nil {
		return nil
	}

	proofPtr, ok := fieldAddr.(**types.AggchainProof)
	if !ok {
		return errors.New("invalid type for AggchainProof")
	}

	data, ok := scanTarget.(*[]byte)
	if !ok || data == nil || *data == nil {
		return nil // No data to decode
	}

	return json.Unmarshal(*data, proofPtr)
}

// PreWrite prepares the field for writing to the database.
func (m *AggchainProofMeddler) PreWrite(field interface{}) (saveValue interface{}, err error) {
	if field == nil {
		return nil, nil
	}

	proof, ok := field.(*types.AggchainProof)
	if !ok {
		return nil, errors.New("invalid type for AggchainProof")
	}

	return json.Marshal(proof)
}
