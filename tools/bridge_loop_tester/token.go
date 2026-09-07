package bridgelooptester

import (
	"context"
	"fmt"
	"math/big"

	"github.com/agglayer/aggkit/test/contracts/mintableerc20"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// ERC20 method names, packed through the mintableerc20 ABI so every write goes through
// NetworkClient's serialized sender instead of the binding's own transactor.
const (
	methodMint    = "mint"
	methodApprove = "approve"
)

// Token is the subset of ERC20 behaviour the loop tester needs on one network: read balances and
// allowances, mint (the test ERC20 is freely mintable) and approve the bridge. Every method is safe
// for concurrent use; the write methods inherit NetworkClient.SendTx's nonce serialization.
type Token interface {
	// Address returns the token contract's address on this network.
	Address() common.Address
	// BalanceOf returns account's token balance at the latest block.
	BalanceOf(ctx context.Context, account common.Address) (*big.Int, error)
	// Allowance returns how much of owner's balance spender may move.
	Allowance(ctx context.Context, owner, spender common.Address) (*big.Int, error)
	// Mint mints amount to to. Only the freely-mintable test ERC20 supports this; a bridge-created
	// wrapped representation does not, and will revert.
	Mint(ctx context.Context, to common.Address, amount *big.Int) (*ethtypes.Receipt, error)
	// Approve approves spender for amount.
	Approve(ctx context.Context, spender common.Address, amount *big.Int) (*ethtypes.Receipt, error)
}

// erc20Token is the Token implementation.
type erc20Token struct {
	client   NetworkClient
	address  common.Address
	abi      *abi.ABI
	contract *mintableerc20.Mintableerc20
}

var _ Token = (*erc20Token)(nil)

// NewToken binds the ERC20 at address on client's network. Use it both for the deployed test token
// and for its bridge-created wrapped representation on another network (whose address comes from
// Bridge.GetTokenWrappedAddress).
func NewToken(client NetworkClient, address common.Address) (Token, error) {
	if client == nil {
		return nil, fmt.Errorf("new token: network client is required")
	}
	if address == (common.Address{}) {
		return nil, fmt.Errorf("new token on %s: token address must not be the zero address", client.Name())
	}

	parsed, err := mintableerc20.Mintableerc20MetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("new token on %s: parse ERC20 ABI: %w", client.Name(), err)
	}

	contract, err := mintableerc20.NewMintableerc20(address, client.Backend())
	if err != nil {
		return nil, fmt.Errorf("new token on %s: bind ERC20 at %s: %w", client.Name(), address, err)
	}

	return &erc20Token{client: client, address: address, abi: parsed, contract: contract}, nil
}

// DeployToken deploys the mintableerc20 test ERC20 on client's network and returns a Token bound to
// it, plus the deployment transaction's hash (for TokenState.DeployTxHash's traceability, see
// (*Orchestrator).DeployToken and ensureTokens). The deployment goes through NetworkClient.SendTx,
// so it shares the serialized nonce sequence with everything else this signer sends.
func DeployToken(ctx context.Context, client NetworkClient, name, symbol string) (Token, common.Hash, error) {
	if client == nil {
		return nil, common.Hash{}, fmt.Errorf("deploy token: network client is required")
	}

	parsed, err := mintableerc20.Mintableerc20MetaData.GetAbi()
	if err != nil {
		return nil, common.Hash{}, fmt.Errorf("deploy token on %s: parse ERC20 ABI: %w", client.Name(), err)
	}

	args, err := parsed.Pack("", name, symbol)
	if err != nil {
		return nil, common.Hash{}, fmt.Errorf("deploy token on %s: pack constructor arguments: %w",
			client.Name(), err)
	}

	creation := append(common.FromHex(mintableerc20.Mintableerc20MetaData.Bin), args...)

	receipt, err := client.SendTx(ctx, TxRequest{Label: "deploy mintableerc20", Data: creation})
	if err != nil {
		return nil, common.Hash{}, err
	}
	if receipt.ContractAddress == (common.Address{}) {
		return nil, common.Hash{}, fmt.Errorf("deploy token on %s: tx %s was mined without a contract address",
			client.Name(), receipt.TxHash)
	}

	token, err := NewToken(client, receipt.ContractAddress)
	if err != nil {
		return nil, common.Hash{}, err
	}

	return token, receipt.TxHash, nil
}

// Address returns the token contract's address on this network.
func (t *erc20Token) Address() common.Address { return t.address }

// BalanceOf returns account's token balance at the latest block.
func (t *erc20Token) BalanceOf(ctx context.Context, account common.Address) (*big.Int, error) {
	balance, err := t.contract.BalanceOf(&bind.CallOpts{Context: ctx}, account)
	if err != nil {
		return nil, fmt.Errorf("read balanceOf(%s) on token %s (%s): %w",
			account, t.address, t.client.Name(), err)
	}

	return balance, nil
}

// Allowance returns how much of owner's balance spender may move.
func (t *erc20Token) Allowance(ctx context.Context, owner, spender common.Address) (*big.Int, error) {
	allowance, err := t.contract.Allowance(&bind.CallOpts{Context: ctx}, owner, spender)
	if err != nil {
		return nil, fmt.Errorf("read allowance(%s, %s) on token %s (%s): %w",
			owner, spender, t.address, t.client.Name(), err)
	}

	return allowance, nil
}

// Mint mints amount to to.
func (t *erc20Token) Mint(
	ctx context.Context, to common.Address, amount *big.Int,
) (*ethtypes.Receipt, error) {
	return t.send(ctx, methodMint, to, amountOf(amount))
}

// Approve approves spender for amount.
func (t *erc20Token) Approve(
	ctx context.Context, spender common.Address, amount *big.Int,
) (*ethtypes.Receipt, error) {
	return t.send(ctx, methodApprove, spender, amountOf(amount))
}

// send packs an ERC20 method call and submits it through the serialized sender.
func (t *erc20Token) send(
	ctx context.Context, method string, target common.Address, amount *big.Int,
) (*ethtypes.Receipt, error) {
	data, err := t.abi.Pack(method, target, amount)
	if err != nil {
		return nil, fmt.Errorf("pack %s calldata for token %s (%s): %w",
			method, t.address, t.client.Name(), err)
	}

	return t.client.SendTx(ctx, TxRequest{
		Label: fmt.Sprintf("erc20 %s", method),
		To:    &t.address,
		Data:  data,
	})
}
