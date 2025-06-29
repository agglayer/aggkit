package helpers

import (
	"context"
	"encoding/hex"
	"fmt"
	big "math/big"
	"testing"

	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/stretchr/testify/mock"
)

func NewEthTxManMock(
	t *testing.T,
	client *simulated.Backend,
	auth *bind.TransactOpts,
) *EthTxManager {
	t.Helper()

	ethTxMock := NewEthTxManager(t)

	ethTxMock.EXPECT().
		Add(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(
			func(ctx context.Context, to *common.Address, value *big.Int,
				data []byte, gasOffset uint64, sidecar *types.BlobTxSidecar) {
				log.Debugf("receiver %s, data: %s", to, hex.EncodeToString(data))

				msg := ethereum.CallMsg{
					From: auth.From,
					To:   to,
					Data: data,
				}

				_, err := client.Client().EstimateGas(ctx, msg)
				if err != nil {
					log.Errorf("eth_estimateGas invocation failed: %w", ExtractRPCErrorData(err))

					res, err := client.Client().CallContract(ctx, msg, nil)
					if err != nil {
						log.Errorf("eth_call invocation failed: %w", ExtractRPCErrorData(err))
					} else {
						log.Debugf("contract call result: %s", hex.EncodeToString(res))
					}
					return
				}

				err = SendTx(ctx, client, auth, to, data, common.Big0)
				if err != nil {
					log.Errorf("failed to send transaction: %w", err)
					return
				}
			}).
		Return(common.Hash{}, nil)
	ethTxMock.EXPECT().
		Result(mock.Anything, mock.Anything).
		Return(ethtxtypes.MonitoredTxResult{Status: ethtxtypes.MonitoredTxStatusMined}, nil)
	ethTxMock.EXPECT().From().Return(auth.From)

	return ethTxMock
}

// SendTx is a helper function that creates the legacy transaction, sings it and sends against simulated environment
func SendTx(ctx context.Context, client *simulated.Backend, auth *bind.TransactOpts,
	to *common.Address, data []byte, value *big.Int) error {
	nonce, err := client.Client().PendingNonceAt(ctx, auth.From)
	if err != nil {
		return err
	}

	gas := uint64(21000) //nolint:mnd

	if len(data) > 0 {
		msg := ethereum.CallMsg{
			From:  auth.From,
			To:    to,
			Data:  data,
			Value: value,
		}

		gas, err = client.Client().EstimateGas(ctx, msg)
		if err != nil {
			return ExtractRPCErrorData(err)
		}
	}

	price, err := client.Client().SuggestGasPrice(ctx)
	if err != nil {
		return err
	}

	senderBalance, err := client.Client().BalanceAt(ctx, auth.From, nil)
	if err != nil {
		return err
	}

	required := new(big.Int).Add(value, new(big.Int).Mul(big.NewInt(int64(gas)), price))
	if senderBalance.Cmp(required) < 0 {
		return fmt.Errorf("insufficient balance: have %s, need %s", senderBalance, required)
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		GasPrice: price,
		Gas:      gas,
		To:       to,
		Value:    value,
		Data:     data,
	})

	signedTx, err := auth.Signer(auth.From, tx)
	if err != nil {
		return err
	}

	err = client.Client().SendTransaction(ctx, signedTx)
	if err != nil {
		return err
	}

	client.Commit()

	return nil
}
