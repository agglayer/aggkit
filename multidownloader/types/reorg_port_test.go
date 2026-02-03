package types

import (
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestCompareBlockHeaders_ExistsRPCBlock(t *testing.T) {
	t.Run("returns false when receiver is nil", func(t *testing.T) {
		var c *CompareBlockHeaders
		result := c.ExistsRPCBlock()
		require.False(t, result)
	})

	t.Run("returns false when RpcHeader is nil", func(t *testing.T) {
		c := &CompareBlockHeaders{
			BlockNumber:   100,
			StorageHeader: &aggkittypes.BlockHeader{Number: 100},
			RpcHeader:     nil,
		}
		result := c.ExistsRPCBlock()
		require.False(t, result)
	})

	t.Run("returns true when RpcHeader is not nil", func(t *testing.T) {
		c := &CompareBlockHeaders{
			BlockNumber: 100,
			RpcHeader: &aggkittypes.BlockHeader{
				Number: 100,
				Hash:   common.HexToHash("0x1234"),
			},
		}
		result := c.ExistsRPCBlock()
		require.True(t, result)
	})
}

func TestCompareBlockHeaders_ExistsStorageBlock(t *testing.T) {
	t.Run("returns false when receiver is nil", func(t *testing.T) {
		var c *CompareBlockHeaders
		result := c.ExistsStorageBlock()
		require.False(t, result)
	})

	t.Run("returns false when StorageHeader is nil", func(t *testing.T) {
		c := &CompareBlockHeaders{
			BlockNumber:   100,
			StorageHeader: nil,
			RpcHeader:     &aggkittypes.BlockHeader{Number: 100},
		}
		result := c.ExistsStorageBlock()
		require.False(t, result)
	})

	t.Run("returns true when StorageHeader is not nil", func(t *testing.T) {
		c := &CompareBlockHeaders{
			BlockNumber: 100,
			StorageHeader: &aggkittypes.BlockHeader{
				Number: 100,
				Hash:   common.HexToHash("0x5678"),
			},
		}
		result := c.ExistsStorageBlock()
		require.True(t, result)
	})
}
