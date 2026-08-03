package claimer

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// decimalBase is the base used to parse decimal amount strings from the certificate.
const decimalBase = 10

// tokenInfo mirrors the token_info object of a bridge exit in the signed certificate.
type tokenInfo struct {
	OriginNetwork      uint32         `json:"origin_network"`
	OriginTokenAddress common.Address `json:"origin_token_address"`
}

// bridgeExit mirrors a single entry of the bridge_exits array in the signed certificate.
type bridgeExit struct {
	LeafType           string         `json:"leaf_type"`
	TokenInfo          tokenInfo      `json:"token_info"`
	DestinationNetwork uint32         `json:"dest_network"`
	DestinationAddress common.Address `json:"dest_address"`
	Amount             string         `json:"amount"`
	Metadata           string         `json:"metadata"`
}

// signedCertificate mirrors the fields of exit-certificate-signed.json that this tool needs.
type signedCertificate struct {
	NetworkID           uint32       `json:"network_id"`
	PrevLocalExitRoot   common.Hash  `json:"prev_local_exit_root"`
	NewLocalExitRoot    common.Hash  `json:"new_local_exit_root"`
	BridgeExits         []bridgeExit `json:"bridge_exits"`
	L1InfoTreeLeafCount uint32       `json:"l1_info_tree_leaf_count"`
}

// Certificate is the parsed, validated view of the signed exit certificate. Each bridge exit is
// normalized into a leaf so it can be matched against the local exit tree by leaf hash.
type Certificate struct {
	NetworkID        uint32
	NewLocalExitRoot common.Hash
	Leaves           []CertificateLeaf
}

// CertificateLeaf is a single bridge exit normalized to the canonical exit-tree leaf form.
type CertificateLeaf struct {
	LeafType           uint8
	OriginNetwork      uint32
	OriginTokenAddress common.Address
	DestinationNetwork uint32
	DestinationAddress common.Address
	Amount             *big.Int
	// MetadataHash is the keccak256 hash of the raw bridge metadata, not the raw metadata itself
	// (the certificate stores it already hashed — see Hash). It is used directly as the leaf's
	// metadata-hash slot.
	MetadataHash []byte
}

// Hash returns the exit-tree leaf hash of the bridge exit. It mirrors the on-chain bridge leaf
// hashing (bridgesync.Bridge.Hash / bridgesyncerlite.BridgeLeaf.Hash) with one crucial difference:
// those compute the metadata-hash slot as crypto.Keccak256(rawMetadata), whereas the certificate's
// Metadata field is ALREADY that hash. exit_certificate Step I applies crypto.Keccak256 to the raw
// BridgeEvent metadata before storing it in BridgeExit.Metadata (matching aggsender, so that
// agglayer's BridgeExit.Hash matches). We therefore use Metadata directly as the metadata-hash slot
// — re-hashing it here would double-hash and never match the local exit tree. This replicates
// agglayer BridgeExit.Hash, including the empty-metadata → EmptyBytesHash fallback.
func (l CertificateLeaf) Hash() common.Hash {
	const (
		uint32ByteSize = 4
		bigIntSize     = 32
	)
	origNet := make([]byte, uint32ByteSize)
	binary.BigEndian.PutUint32(origNet, l.OriginNetwork)
	destNet := make([]byte, uint32ByteSize)
	binary.BigEndian.PutUint32(destNet, l.DestinationNetwork)

	metaHash := l.MetadataHash
	if len(metaHash) == 0 {
		metaHash = aggkitcommon.EmptyBytesHash
	}

	amount := l.Amount
	if amount == nil {
		amount = new(big.Int)
	}
	var buf [bigIntSize]byte

	return crypto.Keccak256Hash(
		[]byte{l.LeafType},
		origNet,
		l.OriginTokenAddress[:],
		destNet,
		l.DestinationAddress[:],
		amount.FillBytes(buf[:]),
		metaHash,
	)
}

// view converts a leaf into its public representation, enriched with the resolved deposit count.
func (l CertificateLeaf) view(depositCount uint32) BridgeExitView {
	return BridgeExitView{
		LeafType:           l.LeafType,
		OriginNetwork:      l.OriginNetwork,
		OriginTokenAddress: addrHex(l.OriginTokenAddress),
		DestinationNetwork: l.DestinationNetwork,
		DestinationAddress: addrHex(l.DestinationAddress),
		Amount:             bigToString(l.Amount),
		Metadata:           metadataHex(l.MetadataHash),
		DepositCount:       depositCount,
	}
}

// LoadCertificate reads and parses the signed exit certificate from disk.
func LoadCertificate(path string) (*Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading signed certificate %q: %w", path, err)
	}

	var sc signedCertificate
	if err := json.Unmarshal(raw, &sc); err != nil {
		return nil, fmt.Errorf("parsing signed certificate %q: %w", path, err)
	}

	cert := &Certificate{
		NetworkID:        sc.NetworkID,
		NewLocalExitRoot: sc.NewLocalExitRoot,
		Leaves:           make([]CertificateLeaf, 0, len(sc.BridgeExits)),
	}

	for i, be := range sc.BridgeExits {
		leafType, err := parseLeafType(be.LeafType)
		if err != nil {
			return nil, fmt.Errorf("bridge exit %d: %w", i, err)
		}

		amount, ok := new(big.Int).SetString(strings.TrimSpace(be.Amount), decimalBase)
		if !ok {
			return nil, fmt.Errorf("bridge exit %d: invalid amount %q", i, be.Amount)
		}

		metadataHash, err := parseMetadata(be.Metadata)
		if err != nil {
			return nil, fmt.Errorf("bridge exit %d: %w", i, err)
		}

		cert.Leaves = append(cert.Leaves, CertificateLeaf{
			LeafType:           leafType,
			OriginNetwork:      be.TokenInfo.OriginNetwork,
			OriginTokenAddress: be.TokenInfo.OriginTokenAddress,
			DestinationNetwork: be.DestinationNetwork,
			DestinationAddress: be.DestinationAddress,
			Amount:             amount,
			MetadataHash:       metadataHash,
		})
	}

	return cert, nil
}

// parseLeafType maps the certificate's string leaf type to the numeric form used on-chain.
func parseLeafType(s string) (uint8, error) {
	switch s {
	case leafTypeTransferStr:
		return leafTypeAsset, nil
	case leafTypeMessageStr:
		return leafTypeMessage, nil
	default:
		return 0, fmt.Errorf("unknown leaf_type %q", s)
	}
}

// parseMetadata decodes the certificate's metadata hex string (with or without 0x prefix).
// An empty string decodes to empty (not nil) metadata, matching the leaf hashing of an empty blob.
func parseMetadata(s string) ([]byte, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	if s == "" {
		return []byte{}, nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid metadata hex: %w", err)
	}
	return b, nil
}
