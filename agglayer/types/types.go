package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"strings"

	"github.com/agglayer/aggkit/bridgesync"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/tree"
	"github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type CertificateStatus int

const (
	Pending CertificateStatus = iota
	Proven
	Candidate
	InError
	Settled

	nilStr  = "nil"
	nullStr = "null"
	base10  = 10

	EstimatedAggchainProofSize      = 10 * aggkitcommon.KB
	EstimatedAggchainSignatureSize  = 0.07 * aggkitcommon.KB
	EstimatedBridgeExitSize         = 0.09 * aggkitcommon.KB
	EstimatedImportedBridgeExitSize = 2.8 * aggkitcommon.KB

	// GlobalIndexBytesSize denotes the size in bytes when global index gets encoded
	GlobalIndexBytesSize = 9
)

var (
	NonSettledStatuses = []CertificateStatus{Pending, Candidate, Proven}
	ClosedStatuses     = []CertificateStatus{Settled, InError}
)

// String representation of the enum
func (c CertificateStatus) String() string {
	return [...]string{"Pending", "Proven", "Candidate", "InError", "Settled"}[c]
}

// IsClosed returns true if the certificate is closed (settled or inError)
func (c CertificateStatus) IsClosed() bool {
	return !c.IsOpen()
}

// IsSettled returns true if the certificate is settled
func (c CertificateStatus) IsSettled() bool {
	return c == Settled
}

// IsInError returns true if the certificate is in error
func (c CertificateStatus) IsInError() bool {
	return c == InError
}

// IsOpen returns true if the certificate is open (pending, candidate or proven)
func (c CertificateStatus) IsOpen() bool {
	return slices.Contains(NonSettledStatuses, c)
}

// UnmarshalJSON is the implementation of the json.Unmarshaler interface
func (c *CertificateStatus) UnmarshalJSON(rawStatus []byte) error {
	status := strings.Trim(string(rawStatus), "\"")
	if strings.Contains(status, "InError") {
		status = "InError"
	}

	switch status {
	case "Pending":
		*c = Pending
	case "InError":
		*c = InError
	case "Proven":
		*c = Proven
	case "Candidate":
		*c = Candidate
	case "Settled":
		*c = Settled
	default:
		// Maybe the status is numeric:
		var statusInt int
		if _, err := fmt.Sscanf(status, "%d", &statusInt); err == nil {
			*c = CertificateStatus(statusInt)
		} else {
			return fmt.Errorf("invalid status: %s", status)
		}
	}

	return nil
}

type AggchainData interface {
	json.Marshaler
	json.Unmarshaler
}

// AggchainDataSelector is a helper struct that allow to decice which type of aggchain data to unmarshal
type AggchainDataSelector struct {
	obj AggchainData
}

func (a *AggchainDataSelector) GetObject() AggchainData {
	return a.obj
}

// UnmarshalJSON is the implementation of the json.Unmarshaler interface
func (a *AggchainDataSelector) UnmarshalJSON(data []byte) error {
	var obj map[string]interface{}
	if string(data) == nullStr {
		return nil
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	var ok bool
	if _, ok = obj["aggchain_data_proof"]; ok {
		a.obj = &AggchainDataProof{}
	} else if _, ok = obj["aggchain_data_signature"]; ok {
		a.obj = &AggchainDataSignature{}
	} else if _, ok = obj["aggchain_data_multisig_with_proof"]; ok {
		a.obj = &AggchainDataMultisigWithProof{}
	} else if _, ok = obj["aggchain_data_multisig"]; ok {
		a.obj = &AggchainDataMultisig{}
	} else {
		return errors.New("invalid aggchain_data type")
	}

	return json.Unmarshal(data, &a.obj)
}

// AggchainDataSignature is the data structure that will hold the signature
// of the aggsender key that signed the certificate
// This is used in the regular PP path
type AggchainDataSignature struct {
	Signature []byte `json:"signature"`
}

// MarshalJSON is the implementation of the json.Marshaler interface
func (a *AggchainDataSignature) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		AggchainDataSignature struct {
			Signature string `json:"signature"`
		} `json:"aggchain_data_signature"`
	}{
		AggchainDataSignature: struct {
			Signature string `json:"signature"`
		}{
			Signature: common.Bytes2Hex(a.Signature),
		},
	})
}

// UnmarshalJSON is the implementation of the json.Unmarshaler interface
func (a *AggchainDataSignature) UnmarshalJSON(data []byte) error {
	aux := &struct {
		AggchainDataSignature struct {
			Signature string `json:"signature"`
		} `json:"aggchain_data_signature"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	a.Signature = common.Hex2Bytes(aux.AggchainDataSignature.Signature)
	return nil
}

// AggchainDataProof is the data structure that will hold the proof of the certificate
// This is used in the aggchain prover path
type AggchainDataProof struct {
	Proof          []byte            `json:"proof"`
	Version        string            `json:"version"`
	Vkey           []byte            `json:"vkey"`
	AggchainParams common.Hash       `json:"aggchain_params"`
	Context        map[string][]byte `json:"context"`
	Signature      []byte            `json:"signature"`
}

// MarshalJSON is the implementation of the json.Marshaler interface
func (a *AggchainDataProof) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		AggchainDataProof struct {
			Proof          string            `json:"proof"`
			AggchainParams string            `json:"aggchain_params"`
			Context        map[string][]byte `json:"context"`
			Version        string            `json:"version"`
			VKey           string            `json:"vkey"`
			Signature      string            `json:"signature"`
		} `json:"aggchain_data_proof"`
	}{
		AggchainDataProof: struct {
			Proof          string            `json:"proof"`
			AggchainParams string            `json:"aggchain_params"`
			Context        map[string][]byte `json:"context"`
			Version        string            `json:"version"`
			VKey           string            `json:"vkey"`
			Signature      string            `json:"signature"`
		}{
			Proof:          common.Bytes2Hex(a.Proof),
			AggchainParams: a.AggchainParams.String(),
			Context:        a.Context,
			Version:        a.Version,
			VKey:           common.Bytes2Hex(a.Vkey),
			Signature:      common.Bytes2Hex(a.Signature),
		},
	})
}

// UnmarshalJSON is the implementation of the json.Unmarshaler interface
func (a *AggchainDataProof) UnmarshalJSON(data []byte) error {
	aux := &struct {
		AggchainDataProof struct {
			Proof          string            `json:"proof"`
			AggchainParams string            `json:"aggchain_params"`
			Context        map[string][]byte `json:"context"`
			Version        string            `json:"version"`
			VKey           string            `json:"vkey"`
			Signature      string            `json:"signature"`
		} `json:"aggchain_data_proof"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	a.Proof = common.Hex2Bytes(aux.AggchainDataProof.Proof)
	a.AggchainParams = common.HexToHash(aux.AggchainDataProof.AggchainParams)
	a.Context = aux.AggchainDataProof.Context
	a.Version = aux.AggchainDataProof.Version
	a.Vkey = common.Hex2Bytes(aux.AggchainDataProof.VKey)
	a.Signature = common.Hex2Bytes(aux.AggchainDataProof.Signature)

	return nil
}

// AggchainDataMultisig is the data structure that will hold the multisig information
// for PP networks
type AggchainDataMultisig struct {
	Multisig *Multisig `json:"multisig"`
}

// MarshalJSON is the implementation of the json.Marshaler interface
func (a *AggchainDataMultisig) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		AggchainDataMultisig struct {
			Multisig *Multisig `json:"multisig"`
		} `json:"aggchain_data_multisig"`
	}{
		AggchainDataMultisig: struct {
			Multisig *Multisig `json:"multisig"`
		}{
			Multisig: a.Multisig,
		},
	})
}

// UnmarshalJSON is the implementation of the json.Unmarshaler interface
func (a *AggchainDataMultisig) UnmarshalJSON(data []byte) error {
	aux := &struct {
		AggchainDataMultisig struct {
			Multisig *Multisig `json:"multisig"`
		} `json:"aggchain_data_multisig"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	a.Multisig = aux.AggchainDataMultisig.Multisig
	return nil
}

// AggchainDataMultisigWithProof is the data structure that will hold the
// multisig and aggchain proof information for FEP networks
type AggchainDataMultisigWithProof struct {
	Multisig      *Multisig          `json:"multisig"`
	AggchainProof *AggchainDataProof `json:"aggchain_proof"`
}

// MarshalJSON is the implementation of the json.Marshaler interface
func (a *AggchainDataMultisigWithProof) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		AggchainDataMultisigWithProof struct {
			Multisig      *Multisig          `json:"multisig"`
			AggchainProof *AggchainDataProof `json:"aggchain_proof"`
		} `json:"aggchain_data_multisig_with_proof"`
	}{
		AggchainDataMultisigWithProof: struct {
			Multisig      *Multisig          `json:"multisig"`
			AggchainProof *AggchainDataProof `json:"aggchain_proof"`
		}{
			Multisig:      a.Multisig,
			AggchainProof: a.AggchainProof,
		},
	})
}

// UnmarshalJSON is the implementation of the json.Unmarshaler interface
func (a *AggchainDataMultisigWithProof) UnmarshalJSON(data []byte) error {
	aux := &struct {
		AggchainDataMultisigWithProof struct {
			Multisig      *Multisig          `json:"multisig"`
			AggchainProof *AggchainDataProof `json:"aggchain_proof"`
		} `json:"aggchain_data_multisig_with_proof"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	a.Multisig = aux.AggchainDataMultisigWithProof.Multisig
	a.AggchainProof = aux.AggchainDataMultisigWithProof.AggchainProof
	return nil
}

// ECDSAMultisigEntry is the data structure that will hold the information
// about a single signature in the multisig
type ECDSAMultisigEntry struct {
	Index     uint32 `json:"index"`
	Signature []byte `json:"signature"`
}

// MarshalJSON is the implementation of the json.Marshaler interface
func (e *ECDSAMultisigEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		Index     uint32 `json:"index"`
		Signature string `json:"signature"`
	}{
		Index:     e.Index,
		Signature: common.Bytes2Hex(e.Signature),
	})
}

// UnmarshalJSON is the implementation of the json.Unmarshaler interface
func (e *ECDSAMultisigEntry) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Index     uint32 `json:"index"`
		Signature string `json:"signature"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	e.Index = aux.Index
	e.Signature = common.Hex2Bytes(aux.Signature)

	return nil
}

// Multisig is the data structure that will hold the multisig information
type Multisig struct {
	Signatures []ECDSAMultisigEntry `json:"signatures"`
}

// Certificate is the data structure that will be sent to the agglayer
type Certificate struct {
	NetworkID           uint32                `json:"network_id"`
	Height              uint64                `json:"height"`
	PrevLocalExitRoot   common.Hash           `json:"prev_local_exit_root"`
	NewLocalExitRoot    common.Hash           `json:"new_local_exit_root"`
	BridgeExits         []*BridgeExit         `json:"bridge_exits"`
	ImportedBridgeExits []*ImportedBridgeExit `json:"imported_bridge_exits"`
	CustomChainData     []byte                `json:"custom_chain_data,omitempty"`
	AggchainData        AggchainData          `json:"aggchain_data,omitempty"`
	L1InfoTreeLeafCount uint32                `json:"l1_info_tree_leaf_count,omitempty"`
}

// Validate certificate
func (c *Certificate) Validate() error {
	if c == nil {
		return errors.New("certificate is nil")
	}
	for i, bridgeExit := range c.BridgeExits {
		if err := bridgeExit.Validate(); err != nil {
			return fmt.Errorf("bridge exit %d is invalid: %w", i, err)
		}
	}
	for i, importedBridgeExit := range c.ImportedBridgeExits {
		if err := importedBridgeExit.Validate(); err != nil {
			return fmt.Errorf("imported bridge exit %d is invalid: %w", i, err)
		}
	}
	return nil
}

// UnmarshalJSON is the implementation of the json.Unmarshaler interface
func (c *Certificate) UnmarshalJSON(data []byte) error {
	aux := &struct {
		NetworkID           uint32                `json:"network_id"`
		Height              uint64                `json:"height"`
		PrevLocalExitRoot   common.Hash           `json:"prev_local_exit_root"`
		NewLocalExitRoot    common.Hash           `json:"new_local_exit_root"`
		BridgeExits         []*BridgeExit         `json:"bridge_exits"`
		ImportedBridgeExits []*ImportedBridgeExit `json:"imported_bridge_exits"`
		CustomChainData     []byte                `json:"custom_chain_data,omitempty"`
		AggchainData        AggchainDataSelector  `json:"aggchain_data,omitempty"`
		L1InfoTreeLeafCount uint32                `json:"l1_info_tree_leaf_count,omitempty"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	c.NetworkID = aux.NetworkID
	c.Height = aux.Height
	c.PrevLocalExitRoot = aux.PrevLocalExitRoot
	c.NewLocalExitRoot = aux.NewLocalExitRoot
	c.BridgeExits = aux.BridgeExits
	c.ImportedBridgeExits = aux.ImportedBridgeExits
	c.CustomChainData = aux.CustomChainData
	c.AggchainData = aux.AggchainData.GetObject()
	c.L1InfoTreeLeafCount = aux.L1InfoTreeLeafCount

	return nil
}

// ID returns a string with the ident of this cert (height/certID)
func (c *Certificate) ID() string {
	if c == nil {
		return "cert{" + nilStr + "}"
	}
	return fmt.Sprintf("cert{height:%d, networkID:%d}", c.Height, c.NetworkID)
}

// Brief returns a string with a brief cert
func (c *Certificate) Brief() string {
	if c == nil {
		return nilStr
	}
	res := fmt.Sprintf("agglayer.Cert {height: %d prevLER: %s, newLER: %s, "+
		"exits: %d imported_exits: %d}",
		c.Height, c.PrevLocalExitRoot.String(), c.NewLocalExitRoot.String(),
		len(c.BridgeExits), len(c.ImportedBridgeExits))
	return res
}

// CertificateID returns a certificateID that identifies the certificate
// next fields are not included: CustomChainData, AggchainData, L1InfoTreeLeafCount
func (c *Certificate) CertificateID() common.Hash {
	bridgeExitsHashes := make([][]byte, len(c.BridgeExits))
	for i, bridgeExit := range c.BridgeExits {
		bridgeExitsHashes[i] = bridgeExit.Hash().Bytes()
	}

	importedBridgeExitsHashes := make([][]byte, len(c.ImportedBridgeExits))
	for i, importedBridgeExit := range c.ImportedBridgeExits {
		importedBridgeExitsHashes[i] = importedBridgeExit.Hash().Bytes()
	}

	bridgeExitsPart := crypto.Keccak256(bridgeExitsHashes...)
	importedBridgeExitsPart := crypto.Keccak256(importedBridgeExitsHashes...)

	return crypto.Keccak256Hash(
		aggkitcommon.Uint32ToBigEndianBytes(c.NetworkID),
		aggkitcommon.Uint64ToBigEndianBytes(c.Height),
		c.PrevLocalExitRoot.Bytes(),
		c.NewLocalExitRoot.Bytes(),
		bridgeExitsPart,
		importedBridgeExitsPart,
		aggkitcommon.ZeroHash.Bytes(), // zero hash is used instead of old Metadata field
	)
}

// ExtractAggchainParams extracts the aggchain params field from the certificate
// with handling different types of aggchain data.
func (c *Certificate) ExtractAggchainParams() []byte {
	switch data := c.AggchainData.(type) {
	case *AggchainDataProof:
		return data.AggchainParams.Bytes()

	case *AggchainDataMultisigWithProof:
		if data.AggchainProof != nil {
			return data.AggchainProof.AggchainParams.Bytes()
		}
	}

	return aggkitcommon.ZeroHash.Bytes()
}

// SignedCertificate is the struct that contains the certificate and the signature of the signer
// NOTE: this is an old and deprecated struct, only to be used for backward compatibility
type SignedCertificate struct {
	*Certificate
	Signature *Signature `json:"signature"`
}

func (s *SignedCertificate) Brief() string {
	return fmt.Sprintf("Certificate:%s,\nSignature: %s", s.Certificate.Brief(), s.Signature.String())
}

// CopyWithDefaulting returns a shallow copy of the signed certificate
func (s *SignedCertificate) CopyWithDefaulting() *SignedCertificate {
	certificateCopy := *s.Certificate

	if certificateCopy.BridgeExits == nil {
		certificateCopy.BridgeExits = make([]*BridgeExit, 0)
	}

	if certificateCopy.ImportedBridgeExits == nil {
		certificateCopy.ImportedBridgeExits = make([]*ImportedBridgeExit, 0)
	}

	signature := s.Signature
	if signature == nil {
		signature = &Signature{}
	}

	return &SignedCertificate{
		Certificate: &certificateCopy,
		Signature:   signature,
	}
}

// Signature is the data structure that will hold the signature of the given certificate
type Signature struct {
	R         common.Hash `json:"r"`
	S         common.Hash `json:"s"`
	OddParity bool        `json:"odd_y_parity"`
}

func (s *Signature) String() string {
	return fmt.Sprintf("R: %s, S: %s, OddParity: %t", s.R.String(), s.S.String(), s.OddParity)
}

// TokenInfo encapsulates the information to uniquely identify a token on the origin network.
type TokenInfo struct {
	OriginNetwork      uint32         `json:"origin_network"`
	OriginTokenAddress common.Address `json:"origin_token_address"`
}

// Validate checks if the TokenInfo is valid
func (t *TokenInfo) Validate() error {
	if t == nil {
		return errors.New("tokenInfo is nil")
	}
	return nil
}

// String returns a string representation of the TokenInfo struct
func (t *TokenInfo) String() string {
	return fmt.Sprintf("OriginNetwork: %d, OriginTokenAddress: %s", t.OriginNetwork, t.OriginTokenAddress.String())
}

// GlobalIndex represents the global index of an imported bridge exit
type GlobalIndex struct {
	MainnetFlag bool   `json:"mainnet_flag"`
	RollupIndex uint32 `json:"rollup_index"`
	LeafIndex   uint32 `json:"leaf_index"`
}

// ToBigInt converts the GlobalIndex to a big.Int representation
func (g *GlobalIndex) ToBigInt() *big.Int {
	return bridgesync.GenerateGlobalIndex(g.MainnetFlag, g.RollupIndex, g.LeafIndex)
}

func (g *GlobalIndex) Validate() error {
	if g == nil {
		return errors.New("globalIndex is nil")
	}
	return nil
}

// String returns a string representation of the GlobalIndex struct
func (g *GlobalIndex) String() string {
	return fmt.Sprintf("MainnetFlag: %t, RollupIndex: %d, LeafIndex: %d", g.MainnetFlag, g.RollupIndex, g.LeafIndex)
}

func (g *GlobalIndex) Hash() common.Hash {
	return crypto.Keccak256Hash(
		aggkitcommon.BigIntToLittleEndianBytes(
			bridgesync.GenerateGlobalIndex(g.MainnetFlag, g.RollupIndex, g.LeafIndex),
		),
	)
}

func (g *GlobalIndex) UnmarshalFromMap(data map[string]interface{}) error {
	rollupIndex, err := convertMapValue[uint32](data, "rollup_index")
	if err != nil {
		return err
	}

	leafIndex, err := convertMapValue[uint32](data, "leaf_index")
	if err != nil {
		return err
	}

	mainnetFlag, err := convertMapValue[bool](data, "mainnet_flag")
	if err != nil {
		return err
	}

	g.RollupIndex = rollupIndex
	g.LeafIndex = leafIndex
	g.MainnetFlag = mainnetFlag

	return nil
}

// BridgeExit represents a token bridge exit
type BridgeExit struct {
	LeafType           bridgetypes.LeafType `json:"leaf_type"`
	TokenInfo          *TokenInfo           `json:"token_info"`
	DestinationNetwork uint32               `json:"dest_network"`
	DestinationAddress common.Address       `json:"dest_address"`
	Amount             *big.Int             `json:"amount"`
	Metadata           []byte               `json:"metadata"`
}

func (b *BridgeExit) Validate() error {
	if b == nil {
		return errors.New("bridgeExit is nil")
	}
	if b.LeafType.Uint8() > bridgetypes.LeafTypeMessage.Uint8() {
		return fmt.Errorf("bridgeExit leaf type %d is invalid", b.LeafType.Uint8())
	}
	if b.Amount != nil && b.Amount.Sign() < 0 {
		return fmt.Errorf("bridgeExit amount %s is negative", b.Amount.String())
	}
	if err := b.TokenInfo.Validate(); err != nil {
		return fmt.Errorf("bridgeExit token info is invalid: %w", err)
	}
	return nil
}

func (b *BridgeExit) String() string {
	res := fmt.Sprintf("LeafType: %s, DestinationNetwork: %d, DestinationAddress: %s, Amount: %s, Metadata: %s",
		b.LeafType.String(), b.DestinationNetwork, b.DestinationAddress.String(),
		b.Amount.String(), common.Bytes2Hex(b.Metadata))

	if b.TokenInfo == nil {
		res += ", TokenInfo: nil"
	} else {
		res += fmt.Sprintf(", TokenInfo: %s", b.TokenInfo.String())
	}

	return res
}

// Hash returns a hash that uniquely identifies the bridge exit
func (b *BridgeExit) Hash() common.Hash {
	if b.Validate() != nil {
		return common.Hash{}
	}
	if b.Amount == nil {
		b.Amount = big.NewInt(0)
	}

	metaDataHash := b.Metadata
	if len(metaDataHash) == 0 {
		metaDataHash = aggkitcommon.EmptyBytesHash
	}

	return crypto.Keccak256Hash(
		[]byte{b.LeafType.Uint8()},
		aggkitcommon.Uint32ToBigEndianBytes(b.TokenInfo.OriginNetwork),
		b.TokenInfo.OriginTokenAddress.Bytes(),
		aggkitcommon.Uint32ToBigEndianBytes(b.DestinationNetwork),
		b.DestinationAddress.Bytes(),
		common.BigToHash(b.Amount).Bytes(),
		metaDataHash,
	)
}

// MarshalJSON is the implementation of the json.Marshaler interface
func (b *BridgeExit) MarshalJSON() ([]byte, error) {
	var metadataString interface{}

	if len(b.Metadata) > 0 {
		metadataString = common.Bytes2Hex(b.Metadata)
	} else {
		metadataString = nil
	}

	return json.Marshal(&struct {
		LeafType           string         `json:"leaf_type"`
		TokenInfo          *TokenInfo     `json:"token_info"`
		DestinationNetwork uint32         `json:"dest_network"`
		DestinationAddress common.Address `json:"dest_address"`
		Amount             string         `json:"amount"`
		Metadata           interface{}    `json:"metadata"`
	}{
		LeafType:           b.LeafType.String(),
		TokenInfo:          b.TokenInfo,
		DestinationNetwork: b.DestinationNetwork,
		DestinationAddress: b.DestinationAddress,
		Amount:             b.Amount.String(),
		Metadata:           metadataString,
	})
}

func (b *BridgeExit) UnmarshalJSON(data []byte) error {
	aux := &struct {
		LeafType           bridgetypes.LeafType `json:"leaf_type"`
		TokenInfo          *TokenInfo           `json:"token_info"`
		DestinationNetwork uint32               `json:"dest_network"`
		DestinationAddress common.Address       `json:"dest_address"`
		Amount             string               `json:"amount"`
		Metadata           interface{}          `json:"metadata"`
	}{}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	b.LeafType = aux.LeafType
	b.TokenInfo = aux.TokenInfo
	b.DestinationNetwork = aux.DestinationNetwork
	b.DestinationAddress = aux.DestinationAddress
	var ok bool
	if !strings.Contains(aux.Amount, nilStr) {
		b.Amount, ok = new(big.Int).SetString(aux.Amount, base10)
		if !ok {
			return fmt.Errorf("failed to convert amount to big.Int: %s", aux.Amount)
		}
	}

	if s, ok := aux.Metadata.(string); ok {
		b.Metadata = common.Hex2Bytes(s)
	} else {
		b.Metadata = nil
	}
	return nil
}

// MerkleProof represents an inclusion proof of a leaf in a Merkle tree
type MerkleProof struct {
	Root  common.Hash                      `json:"root"`
	Proof [types.DefaultHeight]common.Hash `json:"proof"`
}

// MarshalJSON is the implementation of the json.Marshaler interface
func (m *MerkleProof) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		Root  common.Hash                                 `json:"root"`
		Proof map[string][types.DefaultHeight]common.Hash `json:"proof"`
	}{
		Root: m.Root,
		Proof: map[string][types.DefaultHeight]common.Hash{
			"siblings": m.Proof,
		},
	})
}

func (m *MerkleProof) UnmarshalJSON(data []byte) error {
	aux := &struct {
		Root  common.Hash                                 `json:"root"`
		Proof map[string][types.DefaultHeight]common.Hash `json:"proof"`
	}{}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	m.Root = aux.Root
	m.Proof = aux.Proof["siblings"]
	return nil
}

// Hash returns the hash of the Merkle proof struct
func (m *MerkleProof) Hash() common.Hash {
	proofsAsSingleSlice := make([]byte, 0)

	for _, proof := range m.Proof {
		proofsAsSingleSlice = append(proofsAsSingleSlice, proof.Bytes()...)
	}

	return crypto.Keccak256Hash(
		m.Root.Bytes(),
		proofsAsSingleSlice,
	)
}

func (m *MerkleProof) String() string {
	return fmt.Sprintf("Root: %s, Proof: %v", m.Root.String(), m.Proof)
}

// L1InfoTreeLeafInner represents the inner part of the L1 info tree leaf
type L1InfoTreeLeafInner struct {
	GlobalExitRoot common.Hash `json:"global_exit_root"`
	BlockHash      common.Hash `json:"block_hash"`
	Timestamp      uint64      `json:"timestamp"`
}

// Hash returns the hash of the L1InfoTreeLeafInner struct
func (l *L1InfoTreeLeafInner) Hash() common.Hash {
	return crypto.Keccak256Hash(
		l.GlobalExitRoot.Bytes(),
		l.BlockHash.Bytes(),
		aggkitcommon.Uint64ToBigEndianBytes(l.Timestamp),
	)
}

func (l *L1InfoTreeLeafInner) String() string {
	return fmt.Sprintf("GlobalExitRoot: %s, BlockHash: %s, Timestamp: %d",
		l.GlobalExitRoot.String(), l.BlockHash.String(), l.Timestamp)
}

// L1InfoTreeLeaf represents the leaf of the L1 info tree
type L1InfoTreeLeaf struct {
	L1InfoTreeIndex uint32               `json:"l1_info_tree_index"`
	RollupExitRoot  common.Hash          `json:"rer"`
	MainnetExitRoot common.Hash          `json:"mer"`
	Inner           *L1InfoTreeLeafInner `json:"inner"`
}

// Hash returns the hash of the L1InfoTreeLeaf struct
func (l *L1InfoTreeLeaf) Hash() common.Hash {
	return l.Inner.Hash()
}

func (l *L1InfoTreeLeaf) String() string {
	return fmt.Sprintf("L1InfoTreeIndex: %d, RollupExitRoot: %s, MainnetExitRoot: %s, Inner: %s",
		l.L1InfoTreeIndex,
		l.RollupExitRoot.String(),
		l.MainnetExitRoot.String(),
		l.Inner.String(),
	)
}

func (l *L1InfoTreeLeaf) Validate() error {
	if l == nil {
		return errors.New("L1InfoTreeLeaf is nil")
	}
	if l.Inner == nil {
		return errors.New("L1InfoTreeLeaf inner is nil")
	}
	return nil
}

type RemovedGER struct {
	GlobalExitRoot common.Hash `json:"global_exit_root"`
	BlockNumber    uint64      `json:"block_number"`
	LogIndex       uint64      `json:"log_index"`
}

type Unclaim struct {
	GlobalIndex *GlobalIndex `json:"global_index"`
	BlockNumber uint64       `json:"block_number"`
	LogIndex    uint64       `json:"log_index"`
}

type ProvenInsertedGERWithBlockNumber struct {
	BlockNumber           uint64            `json:"block_number"`
	ProvenInsertedGERLeaf ProvenInsertedGER `json:"inserted_ger_leaf"`
	LogIndex              uint64            `json:"log_index"`
}

type ProvenInsertedGER struct {
	ProofGERToL1Root *MerkleProof    `json:"proof_ger_l1root"`
	L1Leaf           *L1InfoTreeLeaf `json:"l1_leaf"`
}

type ImportedBridgeExitWithBlockNumber struct {
	BlockNumber        uint64              `json:"block_number"`
	LogIndex           uint64              `json:"log_index"`
	ImportedBridgeExit *ImportedBridgeExit `json:"imported_bridge_exit"`
}

// String returns a string representation of the ImportedBridgeExitWithBlockNumber struct
func (i *ImportedBridgeExitWithBlockNumber) String() string {
	if i == nil {
		return "ImportedBridgeExitWithBlockNumber{nil}"
	}
	return fmt.Sprintf("BlockNumber: %d, ImportedBridgeExit: %s",
		i.BlockNumber, i.ImportedBridgeExit.String())
}

// Claim is the interface that will be implemented by the different types of claims
type Claim interface {
	Type() string
	Hash() common.Hash
	MarshalJSON() ([]byte, error)
	String() string
	Validate() error
}

// ClaimFromMainnet represents a claim originating from the mainnet
type ClaimFromMainnet struct {
	ProofLeafMER     *MerkleProof    `json:"proof_leaf_mer"`
	ProofGERToL1Root *MerkleProof    `json:"proof_ger_l1root"`
	L1Leaf           *L1InfoTreeLeaf `json:"l1_leaf"`
}

// Type is the implementation of Claim interface
func (c ClaimFromMainnet) Type() string {
	return "Mainnet"
}

// MarshalJSON is the implementation of Claim interface
func (c *ClaimFromMainnet) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		Child map[string]interface{} `json:"Mainnet"`
	}{
		Child: map[string]interface{}{
			"proof_leaf_mer":   c.ProofLeafMER,
			"proof_ger_l1root": c.ProofGERToL1Root,
			"l1_leaf":          c.L1Leaf,
		},
	})
}

func (c *ClaimFromMainnet) UnmarshalJSON(data []byte) error {
	if string(data) == nullStr {
		return nil
	}

	claimData := &struct {
		Child struct {
			ProofLeafMER     *MerkleProof    `json:"proof_leaf_mer"`
			ProofGERToL1Root *MerkleProof    `json:"proof_ger_l1root"`
			L1Leaf           *L1InfoTreeLeaf `json:"l1_leaf"`
		} `json:"Mainnet"`
	}{}
	if err := json.Unmarshal(data, &claimData); err != nil {
		return fmt.Errorf("failed to unmarshal the subobject: %w", err)
	}
	c.ProofLeafMER = claimData.Child.ProofLeafMER
	c.ProofGERToL1Root = claimData.Child.ProofGERToL1Root
	c.L1Leaf = claimData.Child.L1Leaf

	return nil
}

// Hash is the implementation of Claim interface
func (c *ClaimFromMainnet) Hash() common.Hash {
	if c.Validate() != nil {
		return common.Hash{}
	}
	return crypto.Keccak256Hash(
		c.ProofLeafMER.Hash().Bytes(),
		c.ProofGERToL1Root.Hash().Bytes(),
		c.L1Leaf.Hash().Bytes(),
	)
}

func (c *ClaimFromMainnet) String() string {
	return fmt.Sprintf("ProofLeafMER: %s, ProofGERToL1Root: %s, L1Leaf: %s",
		c.ProofLeafMER.String(), c.ProofGERToL1Root.String(), c.L1Leaf.String())
}

func (c *ClaimFromMainnet) Validate() error {
	if c == nil {
		return errors.New("ClaimFromMainnnet is nil")
	}
	if c.ProofLeafMER == nil || c.ProofGERToL1Root == nil {
		return errors.New("ClaimFromMainnnet has nil proofs")
	}
	if err := c.L1Leaf.Validate(); err != nil {
		return fmt.Errorf("ClaimFromMainnnet L1Leaf error: %w", err)
	}
	return nil
}

// VerifyProofs verifies the inclusion proofs for the given mainnet claim
func (c *ClaimFromMainnet) VerifyProofs(l1RootFromWhichToProof, leafHash common.Hash, leafIndex uint32) error {
	if c.ProofGERToL1Root.Root != l1RootFromWhichToProof {
		return fmt.Errorf("ClaimFromMainnnet - Mismatch between the provided L1 root and the inclusion proof. "+
			"GERToL1Root: %s != L1RootFromWhichToProof: %s",
			c.ProofGERToL1Root.Root.String(), l1RootFromWhichToProof.String())
	}

	if c.ProofLeafMER.Root != c.L1Leaf.MainnetExitRoot {
		return fmt.Errorf("ClaimFromMainnnet - Mismatch on the MER between the L1 leaf and the inclusion proof. "+
			"ProofLeafMER: %s != L1Leaf.MainnetExitRoot: %s",
			c.ProofLeafMER.Root.String(), c.L1Leaf.MainnetExitRoot.String())
	}

	if err := tree.VerifyProof(leafHash, c.ProofLeafMER.Proof,
		leafIndex, c.ProofLeafMER.Root); err != nil {
		return fmt.Errorf("ClaimFromMainnnet - Invalid merkle path from the leaf to the LER. "+
			"ProofLeafMER: %s, leafHash: %s, leafIndex: %d, err: %w",
			c.ProofLeafMER.String(), leafHash.String(), leafIndex, err)
	}

	if err := tree.VerifyProof(c.L1Leaf.Hash(), c.ProofGERToL1Root.Proof,
		c.L1Leaf.L1InfoTreeIndex, c.ProofGERToL1Root.Root); err != nil {
		return fmt.Errorf("ClaimFromMainnnet - Invalid merkle path from the GER to the L1 Info Root. "+
			"ProofGERToL1Root: %s, L1Leaf: %s, leafIndex: %d, err: %w",
			c.ProofGERToL1Root.String(), c.L1Leaf.String(), c.L1Leaf.L1InfoTreeIndex, err)
	}

	return nil
}

// ClaimFromRollup represents a claim originating from a rollup
type ClaimFromRollup struct {
	ProofLeafLER     *MerkleProof    `json:"proof_leaf_ler"`
	ProofLERToRER    *MerkleProof    `json:"proof_ler_rer"`
	ProofGERToL1Root *MerkleProof    `json:"proof_ger_l1root"`
	L1Leaf           *L1InfoTreeLeaf `json:"l1_leaf"`
}

// Type is the implementation of Claim interface
func (c ClaimFromRollup) Type() string {
	return "Rollup"
}

// MarshalJSON is the implementation of Claim interface
func (c *ClaimFromRollup) MarshalJSON() ([]byte, error) {
	return json.Marshal(&struct {
		Child map[string]interface{} `json:"Rollup"`
	}{
		Child: map[string]interface{}{
			"proof_leaf_ler":   c.ProofLeafLER,
			"proof_ler_rer":    c.ProofLERToRER,
			"proof_ger_l1root": c.ProofGERToL1Root,
			"l1_leaf":          c.L1Leaf,
		},
	})
}

func (c *ClaimFromRollup) UnmarshalJSON(data []byte) error {
	if string(data) == nullStr {
		return nil
	}

	claimData := &struct {
		Child struct {
			ProofLeafLER     *MerkleProof    `json:"proof_leaf_ler"`
			ProofLERToRER    *MerkleProof    `json:"proof_ler_rer"`
			ProofGERToL1Root *MerkleProof    `json:"proof_ger_l1root"`
			L1Leaf           *L1InfoTreeLeaf `json:"l1_leaf"`
		} `json:"Rollup"`
	}{}

	if err := json.Unmarshal(data, &claimData); err != nil {
		return fmt.Errorf("failed to unmarshal the subobject: %w", err)
	}
	c.ProofLeafLER = claimData.Child.ProofLeafLER
	c.ProofLERToRER = claimData.Child.ProofLERToRER
	c.ProofGERToL1Root = claimData.Child.ProofGERToL1Root
	c.L1Leaf = claimData.Child.L1Leaf

	return nil
}

// Hash is the implementation of Claim interface
func (c *ClaimFromRollup) Hash() common.Hash {
	if c.Validate() != nil {
		return common.Hash{}
	}
	return crypto.Keccak256Hash(
		c.ProofLeafLER.Hash().Bytes(),
		c.ProofLERToRER.Hash().Bytes(),
		c.ProofGERToL1Root.Hash().Bytes(),
		c.L1Leaf.Hash().Bytes(),
	)
}

func (c *ClaimFromRollup) Validate() error {
	if c == nil {
		return errors.New("ClaimFromRollup is nil")
	}
	if c.ProofLeafLER == nil || c.ProofLERToRER == nil || c.ProofGERToL1Root == nil {
		return errors.New("ClaimFromRollup has nil proofs")
	}
	if c.L1Leaf == nil {
		return errors.New("ClaimFromRollup has nil L1Leaf")
	}
	return nil
}

func (c *ClaimFromRollup) String() string {
	return fmt.Sprintf("ProofLeafLER: %s, ProofLERToRER: %s, ProofGERToL1Root: %s, L1Leaf: %s",
		c.ProofLeafLER.String(), c.ProofLERToRER.String(), c.ProofGERToL1Root.String(), c.L1Leaf.String())
}

func (c *ClaimFromRollup) VerifyProofs(
	l1RootFromWhichToProof, leafHash common.Hash, rollupIndex, leafIndex uint32) error {
	if c.ProofGERToL1Root.Root != l1RootFromWhichToProof {
		return fmt.Errorf("ClaimFromRollup - Mismatch between the provided L1 root and the inclusion proof. "+
			"ProofGERToL1Root: %s != L1RootFromWhichToProof: %s",
			c.ProofGERToL1Root.Root.String(), l1RootFromWhichToProof.String())
	}

	if c.ProofLERToRER.Root != c.L1Leaf.RollupExitRoot {
		return fmt.Errorf("ClaimFromRollup - Mismatch on the RER between the L1 leaf and the inclusion proof. "+
			"ProofLERToRER: %s != L1Leaf.RollupExitRoot: %s",
			c.ProofLERToRER.Root.String(), c.L1Leaf.RollupExitRoot.String())
	}

	if err := tree.VerifyProof(leafHash, c.ProofLeafLER.Proof,
		leafIndex, c.ProofLeafLER.Root); err != nil {
		return fmt.Errorf("ClaimFromRollup - Invalid merkle path from the leaf to the LER. "+
			"ProofLeafLER: %s, leafHash: %s, leafIndex: %d, err: %w",
			c.ProofLeafLER.String(), leafHash.String(), leafIndex, err)
	}

	if err := tree.VerifyProof(c.L1Leaf.Hash(), c.ProofGERToL1Root.Proof,
		c.L1Leaf.L1InfoTreeIndex, c.ProofGERToL1Root.Root); err != nil {
		return fmt.Errorf("ClaimFromRollup - Invalid merkle path from the GER to the L1 Info Root. "+
			"ProofGERToL1Root: %s, L1Leaf: %s, leafIndex: %d, err: %w",
			c.ProofGERToL1Root.String(), c.L1Leaf.String(), c.L1Leaf.L1InfoTreeIndex, err)
	}

	if err := tree.VerifyProof(c.ProofLeafLER.Root, c.ProofLERToRER.Proof, rollupIndex, c.ProofLERToRER.Root); err != nil {
		return fmt.Errorf("ClaimFromRollup - Invalid merkle path from the LER to the RER. "+
			"ProofLERToRER: %s, LER: %s, rollupIndex: %d, err: %w",
			c.ProofLERToRER.String(), c.ProofLeafLER.String(), rollupIndex, err)
	}

	return nil
}

// ClaimSelector is a helper struct that allow to decice which type of claim to unmarshal
type ClaimSelector struct {
	obj Claim
}

func (c *ClaimSelector) GetObject() Claim {
	return c.obj
}

func (c *ClaimSelector) UnmarshalJSON(data []byte) error {
	var obj map[string]interface{}
	if string(data) == nullStr {
		return nil
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	var ok bool
	if _, ok = obj["Mainnet"]; ok {
		c.obj = &ClaimFromMainnet{}
	} else if _, ok = obj["Rollup"]; ok {
		c.obj = &ClaimFromRollup{}
	} else {
		return errors.New("invalid claim type")
	}

	return json.Unmarshal(data, &c.obj)
}

// ImportedBridgeExit represents a token bridge exit originating on another network but claimed on the current network.
type ImportedBridgeExit struct {
	BridgeExit  *BridgeExit  `json:"bridge_exit"`
	ClaimData   Claim        `json:"claim_data"`
	GlobalIndex *GlobalIndex `json:"global_index"`
}

func (c *ImportedBridgeExit) String() string {
	var res string

	if c.BridgeExit == nil {
		res = "BridgeExit: nil"
	} else {
		res = fmt.Sprintf("BridgeExit: %s", c.BridgeExit.String())
	}

	if c.GlobalIndex == nil {
		res += ", GlobalIndex: nil"
	} else {
		res += fmt.Sprintf(", GlobalIndex: %s", c.GlobalIndex.String())
	}
	if c.ClaimData != nil {
		res += fmt.Sprintf("ClaimData: %s", c.ClaimData.String())
	} else {
		res += ", ClaimData: nil"
	}

	return res
}

func (c *ImportedBridgeExit) UnmarshalJSON(data []byte) error {
	aux := &struct {
		BridgeExit  *BridgeExit   `json:"bridge_exit"`
		ClaimData   ClaimSelector `json:"claim_data"`
		GlobalIndex *GlobalIndex  `json:"global_index"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	c.BridgeExit = aux.BridgeExit
	c.ClaimData = aux.ClaimData.GetObject()
	c.GlobalIndex = aux.GlobalIndex
	return nil
}

// Hash returns a hash that uniquely identifies the imported bridge exit
func (c *ImportedBridgeExit) Hash() common.Hash {
	return crypto.Keccak256Hash(
		c.BridgeExit.Hash().Bytes(),
		c.ClaimData.Hash().Bytes(),
		c.GlobalIndex.Hash().Bytes(),
	)
}

func (c *ImportedBridgeExit) Validate() error {
	if c == nil {
		return errors.New("ImportedBridgeExit is nil")
	}
	if err := c.BridgeExit.Validate(); err != nil {
		return fmt.Errorf("ImportedBridgeExit.BridgeExit not valid: %w", err)
	}
	if err := c.GlobalIndex.Validate(); err != nil {
		return fmt.Errorf("ImportedBridgeExit.GlobalIndex exit not valid: %w", err)
	}
	return nil
}

// VerifyProofs verifies the inclusion proofs for the imported bridge exit
func (c *ImportedBridgeExit) VerifyProofs(l1RootFromWhichToProve common.Hash) error {
	if c == nil {
		return errors.New("ImportedBridgeExit is nil")
	}

	if c.ClaimData == nil {
		return errors.New("ImportedBridgeExit.ClaimData is nil")
	}

	switch cl := c.ClaimData.(type) {
	case *ClaimFromMainnet:
		if err := cl.VerifyProofs(l1RootFromWhichToProve, c.BridgeExit.Hash(), c.GlobalIndex.LeafIndex); err != nil {
			return fmt.Errorf("ImportedBridgeExit - ClaimFromMainnnet verification failed: %w", err)
		}
		return nil
	case *ClaimFromRollup:
		if err := cl.VerifyProofs(l1RootFromWhichToProve, c.BridgeExit.Hash(),
			c.GlobalIndex.RollupIndex, c.GlobalIndex.LeafIndex); err != nil {
			return fmt.Errorf("ImportedBridgeExit - ClaimFromRollup verification failed: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("ImportedBridgeExit - unknown claim type: %T", c.ClaimData)
	}
}

// GlobalIndexToLittleEndianBytes converts the global index to a byte slice in little-endian format
func (c *ImportedBridgeExit) GlobalIndexToLittleEndianBytes() []byte {
	return aggkitcommon.BigIntToLittleEndianBytes(
		bridgesync.GenerateGlobalIndex(
			c.GlobalIndex.MainnetFlag,
			c.GlobalIndex.RollupIndex,
			c.GlobalIndex.LeafIndex,
		),
	)
}

var _ error = (*GenericError)(nil)

type GenericError struct {
	Key   string
	Value string
}

func (p *GenericError) Error() string {
	return fmt.Sprintf("[Agglayer Error] %s: %s", p.Key, p.Value)
}

// CertificateHeader is the structure returned by the interop_getCertificateHeader RPC call
type CertificateHeader struct {
	NetworkID             uint32            `json:"network_id"`
	Height                uint64            `json:"height"`
	EpochNumber           *uint64           `json:"epoch_number"`
	CertificateIndex      *uint64           `json:"certificate_index"`
	CertificateID         common.Hash       `json:"certificate_id"`
	PreviousLocalExitRoot *common.Hash      `json:"prev_local_exit_root,omitempty"`
	NewLocalExitRoot      common.Hash       `json:"new_local_exit_root"`
	Status                CertificateStatus `json:"status"`
	Metadata              common.Hash       `json:"metadata"`
	Error                 error             `json:"-"`
	SettlementTxHash      *common.Hash      `json:"settlement_tx_hash,omitempty"`
}

// ID returns a string with the ident of this cert (height/certID)
func (c *CertificateHeader) ID() string {
	if c == nil {
		return nilStr
	}
	return fmt.Sprintf("%d/%s", c.Height, c.CertificateID.String())
}

// StatusString returns the string representation of the status
func (c *CertificateHeader) StatusString() string {
	if c == nil {
		return "???"
	}
	return c.Status.String()
}

func (c *CertificateHeader) String() string {
	if c == nil {
		return nilStr
	}
	errors := ""
	if c.Error != nil {
		errors = c.Error.Error()
	}
	previousLocalExitRoot := nilStr
	if c.PreviousLocalExitRoot != nil {
		previousLocalExitRoot = c.PreviousLocalExitRoot.String()
	}
	settlementTxHash := nilStr
	if c.SettlementTxHash != nil {
		settlementTxHash = c.SettlementTxHash.String()
	}
	return fmt.Sprintf("Height: %d, CertificateID: %s, PreviousLocalExitRoot: %s, NewLocalExitRoot: %s. Status: %s."+
		" SettlementTxnHash: %s, Errors: [%s]",
		c.Height, c.CertificateID.String(), previousLocalExitRoot, c.NewLocalExitRoot.String(), c.Status.String(),
		settlementTxHash, errors)
}

func (c *CertificateHeader) UnmarshalJSON(data []byte) error {
	// we define an alias to avoid infinite recursion
	type Alias CertificateHeader
	aux := &struct {
		Status interface{} `json:"status"`
		*Alias
	}{
		Alias: (*Alias)(c),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Process Status field
	switch status := aux.Status.(type) {
	case string: // certificate not InError
		if err := c.Status.UnmarshalJSON([]byte(status)); err != nil {
			return err
		}
	case map[string]interface{}: // certificate has errors
		inErrMap, err := convertMapValue[map[string]interface{}](status, "InError")
		if err != nil {
			return err
		}

		inErrDataMap, err := convertMapValue[map[string]interface{}](inErrMap, "error")
		if err != nil {
			return err
		}

		var agglayerErr error

		for errKey, errValueRaw := range inErrDataMap {
			if errValueJSON, err := json.Marshal(errValueRaw); err != nil {
				agglayerErr = &GenericError{
					Key: errKey,
					Value: fmt.Sprintf("failed to marshal the agglayer error to the JSON. Raw value: %+v\nReason: %+v",
						errValueRaw, err),
				}
			} else {
				agglayerErr = &GenericError{Key: errKey, Value: string(errValueJSON)}
			}
		}

		c.Status = InError
		c.Error = agglayerErr
	default:
		return errors.New("invalid status type")
	}

	return nil
}

// convertMapValue converts the value of a key in a map to a target type.
func convertMapValue[T any](data map[string]interface{}, key string) (T, error) {
	value, ok := data[key]
	if !ok {
		var zero T
		return zero, fmt.Errorf("key %s not found in map", key)
	}

	// Try a direct type assertion
	if convertedValue, ok := value.(T); ok {
		return convertedValue, nil
	}

	// If direct assertion fails, handle numeric type conversions
	var target T
	targetType := reflect.TypeOf(target)

	// Check if value is a float64 (default JSON number type) and target is a numeric type
	if floatValue, ok := value.(float64); ok && targetType.Kind() >= reflect.Int && targetType.Kind() <= reflect.Uint64 {
		convertedValue, err := convertNumeric(floatValue, targetType)
		if err != nil {
			return target, fmt.Errorf("conversion error for key %s: %w", key, err)
		}
		return convertedValue.(T), nil //nolint:forcetypeassert
	}

	return target, fmt.Errorf("value of key %s is not of type %T", key, target)
}

// convertNumeric converts a float64 to the specified numeric type.
func convertNumeric(value float64, targetType reflect.Type) (interface{}, error) {
	switch targetType.Kind() {
	case reflect.Int:
		return int(value), nil
	case reflect.Int8:
		return int8(value), nil
	case reflect.Int16:
		return int16(value), nil
	case reflect.Int32:
		return int32(value), nil
	case reflect.Int64:
		return int64(value), nil
	case reflect.Uint:
		return uint(value), nil
	case reflect.Uint8:
		return uint8(value), nil
	case reflect.Uint16:
		return uint16(value), nil
	case reflect.Uint32:
		return uint32(value), nil
	case reflect.Uint64:
		return uint64(value), nil
	case reflect.Float32:
		return float32(value), nil
	case reflect.Float64:
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported target type %v", targetType)
	}
}

// ClockConfiguration represents the configuration of the epoch clock
// returned by the interop_GetEpochConfiguration RPC call
type ClockConfiguration struct {
	EpochDuration uint64 `json:"epoch_duration"`
	GenesisBlock  uint64 `json:"genesis_block"`
}

func (c ClockConfiguration) String() string {
	return fmt.Sprintf("EpochDuration: %d, GenesisBlock: %d", c.EpochDuration, c.GenesisBlock)
}

// NetworkInfo represents the state of the network returned by the interop_getNetworkInfo RPC call
type NetworkInfo struct {
	// Status is the current status of the network (e.g., "active", "syncing", "error")
	Status string `json:"network_status"`
	// NetworkType is the aggchain type of network
	NetworkType string `json:"network_type"`
	// NetworkID is the unique identifier of the network
	NetworkID uint32 `json:"network_id"`
	// SettledHeight is the height of the latest settled certificate
	SettledHeight *uint64 `json:"settled_height"`
	// SettledCertificateID is the ID of the latest settled certificate
	SettledCertificateID *common.Hash `json:"settled_certificate_id"`
	// SettledPPRoot pessimistic proof root of the latest settled certificate
	SettledPPRoot *common.Hash `json:"settled_pp_root"`
	// SettledLER is the local exit root of the latest settled certificate
	SettledLER *common.Hash `json:"settled_ler"`
	// SettledLETLeafCount is the leaf count of the latest settled local exit tree
	SettledLETLeafCount *uint64 `json:"settled_let_leaf_count"`
	// SettledImportedBridgeExit is the information about the latest settled claim
	SettledImportedBridgeExit *SettledImportedBridgeExit `json:"settled_claim,omitempty"`
	// LatestPendingHeight is the height of the latest pending certificate
	LatestPendingHeight *uint64 `json:"latest_pending_height"`
	// LatestPendingCertificateID is the status of the latest pending certificate (e.g., "Proven", "Pending", "InError")
	LatestPendingStatus *CertificateStatus `json:"latest_pending_status"`
	// LatestPendingError is the error message of the latest pending certificate, if any
	LatestPendingError string `json:"latest_pending_error"`
	// LatestEpochWithSettlement is the epoch number of the latest settlement
	LatestEpochWithSettlement *uint64 `json:"latest_epoch_with_settlement"`
}

// SettledImportedBridgeExit represents the information about a settled claim
type SettledImportedBridgeExit struct {
	GlobalIndex    *big.Int    `json:"global_index"`
	BridgeExitHash common.Hash `json:"bridge_exit_hash"`
}
