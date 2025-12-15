package etherman

import (
	"context"
	"errors"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/rpc"
)

var (
	_                 aggkittypes.RPCClienter = (*NoopRPCClient)(nil)
	ErrNotImplemented                         = errors.New("not implemented")
)

// NoopRPCClient is no operation implementation for the RPCClienter interface
type NoopRPCClient struct{}

func (c *NoopRPCClient) Call(result any, method string, args ...any) error {
	return ErrNotImplemented
}

func (c *NoopRPCClient) BatchCallContext(ctx context.Context, b []rpc.BatchElem) error {
	return ErrNotImplemented
}

func (c *NoopRPCClient) CallContext(ctx context.Context, result interface{}, method string, args ...interface{}) error {
	return ErrNotImplemented
}
