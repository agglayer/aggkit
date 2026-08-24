// Package demo mounts the current bridge service and the spec-first generated
// server side by side over one set of canned bridge rows, so the two wire
// formats for the same data can be compared directly.
//
// It exists to demonstrate a wire-format defect and the pipeline that prevents
// it. Nothing here is production code: the syncer dependencies are mocks and
// the data is fixed.
package demo

import (
	"math/big"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
)

// MainnetNetworkID is the network id the demo queries with. Bridges that
// originate on L1 encode a mainnet flag into bit 64 of their global index,
// which is what pushes the value past the range JavaScript numbers can hold.
const MainnetNetworkID uint32 = 0

// EtrogUpgradeBlock of zero disables the pre-Etrog legacy global-index
// encoding, so every canned row gets the modern packed encoding. The real
// service reads this from the agglayer manager contract.
const EtrogUpgradeBlock uint64 = 0

// wei is 10^18 -- one whole token. Chosen for the canned amounts because it is
// comfortably above 2^53 and therefore also unrepresentable as a JSON number,
// making `amount` a second witness to the same defect as `global_index`.
func wei(whole int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(whole), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
}

// CannedBridges is the single source of data for both mounted servers. The
// first row is the interesting one: deposit count 5 on an L1-origin bridge
// encodes to global index 2^64+5 = 18446744073709551621, which loses precision
// the moment it is parsed as a double.
func CannedBridges() []*bridgesync.Bridge {
	return []*bridgesync.Bridge{
		{
			BlockNum:           1234,
			BlockPos:           1,
			FromAddress:        addr("0xabc1234567890abcdef1234567890abcdef12340"),
			TxHash:             common.HexToHash("0xdef4567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
			BlockTimestamp:     1684500000,
			LeafType:           0,
			OriginNetwork:      0,
			OriginAddress:      common.HexToAddress("0x0000000000000000000000000000000000000000"),
			DestinationNetwork: 10,
			DestinationAddress: common.HexToAddress("0xdef4567890abcdef1234567890abcdef12345678"),
			Amount:             wei(1),
			Metadata:           []byte{},
			DepositCount:       5,
			TxnSender:          common.HexToAddress("0xabc1234567890abcdef1234567890abcdef12340"),
			ToAddress:          common.HexToAddress("0xF9D64d54D32EE2BDceAAbFA60C4C438E224427d0"),
		},
		{
			BlockNum:           1240,
			BlockPos:           0,
			FromAddress:        addr("0x1111111111111111111111111111111111111111"),
			TxHash:             common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
			BlockTimestamp:     1684500120,
			LeafType:           0,
			OriginNetwork:      0,
			OriginAddress:      common.HexToAddress("0x2222222222222222222222222222222222222222"),
			DestinationNetwork: 10,
			DestinationAddress: common.HexToAddress("0x3333333333333333333333333333333333333333"),
			Amount:             wei(42),
			Metadata:           []byte{0xde, 0xad, 0xbe, 0xef},
			DepositCount:       6,
			TxnSender:          common.HexToAddress("0x1111111111111111111111111111111111111111"),
			ToAddress:          common.HexToAddress("0x4444444444444444444444444444444444444444"),
		},
		{
			BlockNum:           1250,
			BlockPos:           3,
			FromAddress:        nil,
			TxHash:             common.HexToHash("0x5555555555555555555555555555555555555555555555555555555555555555"),
			BlockTimestamp:     1684500240,
			LeafType:           1,
			OriginNetwork:      0,
			OriginAddress:      common.HexToAddress("0x6666666666666666666666666666666666666666"),
			DestinationNetwork: 10,
			DestinationAddress: common.HexToAddress("0x7777777777777777777777777777777777777777"),
			Amount:             wei(7),
			Metadata:           []byte{},
			DepositCount:       7,
			TxnSender:          common.HexToAddress("0x6666666666666666666666666666666666666666"),
			ToAddress:          common.HexToAddress("0x8888888888888888888888888888888888888888"),
		},
	}
}

func addr(hex string) *common.Address {
	a := common.HexToAddress(hex)
	return &a
}
