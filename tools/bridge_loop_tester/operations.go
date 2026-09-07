package bridgelooptester

import (
	"context"
	"fmt"
	"math/big"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// plan is DryRun's whole execution: every read and every fundability assertion the run would make,
// and no transaction at all.
//
// DryRun deliberately stops short of the readiness gates and the claim assertions: those need a
// real deposit on the bridge, so there is nothing honest to check without submitting one. What it
// does prove is everything that can be wrong before the first transaction - a network whose gas
// token is not ether, a signer that cannot fund a hop, an ERC20 loop with no token deployed, a
// proxy that cannot route a network - which is exactly the set of mistakes that otherwise only
// surface minutes into a run.
func (o *Orchestrator) plan(ctx context.Context, loops []Loop) []LoopReport {
	reports := make([]LoopReport, 0, len(loops))

	for _, loop := range loops {
		report := LoopReport{
			Name:               loop.Name,
			Asset:              loop.Asset,
			Amount:             loop.Amount.BigInt(),
			Route:              routeOf(loop),
			TokenOriginNetwork: loop.TokenOriginNetwork,
			ValueLocation:      o.valueLocation(loop),
		}
		if record := o.tokenRecord(loop.Name); record != nil {
			report.TokenAddress = record.Address
			report.TokenDeployed = record.Address != (common.Address{})
		}

		for hopIndex := range loop.Hops {
			plan := o.planHop(ctx, loop, hopIndex, report.TokenAddress)
			report.Plan = append(report.Plan, plan)
			o.logger.Infof("bridge_loop_tester: DRY RUN would bridge loop=%q hop=%d route=%s asset=%s "+
				"amount=%s claim=%s from=%s source_token=%s destination_token=%s source_balance=%s "+
				"fundable=%t note=%q",
				plan.LoopName, plan.HopIndex, plan.Route(), plan.Asset, bigIntString(plan.Amount),
				plan.Claim, plan.From, addressString(plan.SourceTokenAddress),
				addressString(plan.DestinationTokenAddress), bigIntString(plan.SourceBalance),
				plan.Fundable, plan.Note)
		}

		reports = append(reports, report)
	}

	o.logger.Warnf("bridge_loop_tester: DRY RUN complete: no transaction was submitted, so the readiness " +
		"gates and the claim-mode assertions were not exercised - only what can be checked without a " +
		"deposit was")

	return reports
}

// planHop resolves one hop's addresses and balances without submitting anything.
func (o *Orchestrator) planHop(
	ctx context.Context, loop Loop, hopIndex int, tokenAddress common.Address,
) HopPlan {
	hop := loop.Hops[hopIndex]
	plan := HopPlan{
		LoopName:        loop.Name,
		HopIndex:        hopIndex,
		Source:          hop.Source,
		SourceName:      o.networkName(hop.Source),
		Destination:     hop.Destination,
		DestinationName: o.networkName(hop.Destination),
		Claim:           hop.Claim,
		Asset:           loop.Asset,
		Amount:          loop.Amount.BigInt(),
	}

	source, ok := o.pool.network(hop.Source)
	if !ok {
		plan.Note = fmt.Sprintf("network %d has no pooled client", hop.Source)

		return plan
	}
	plan.From = source.Client.From()

	nativeBalance, err := source.Client.NativeBalance(ctx, plan.From)
	if err != nil {
		plan.Note = fmt.Sprintf("read the native balance of %s on %s: %v", plan.From, source.Config.Name, err)

		return plan
	}
	plan.SourceNativeBalance = nativeBalance

	if loop.Asset == AssetETH {
		plan.SourceBalance = nativeBalance
		required := new(big.Int).Add(plan.Amount, source.Config.MinNativeReserve.BigInt())
		plan.Fundable = nativeBalance.Cmp(required) >= 0
		if !plan.Fundable {
			plan.Note = fmt.Sprintf("holds %s wei but the hop needs %s (amount) plus %s (MinNativeReserve)",
				nativeBalance, plan.Amount, source.Config.MinNativeReserve.String())
		}

		return plan
	}

	o.planERC20Hop(ctx, loop, hop, source, tokenAddress, &plan)

	return plan
}

// planERC20Hop resolves an ERC20 hop's source and destination token addresses and the source
// balance. It reports, rather than guesses, when the loop has no token yet: DryRun deploys nothing,
// so a first dry run of an ERC20 loop legitimately has no address to resolve.
func (o *Orchestrator) planERC20Hop(
	ctx context.Context, loop Loop, hop Hop, source *pooledNetwork,
	tokenAddress common.Address, plan *HopPlan,
) {
	if tokenAddress == (common.Address{}) {
		plan.Note = fmt.Sprintf("loop %q has no ERC20 recorded yet and DryRun deploys nothing; run "+
			"`deploy-token --loop %s` first, or run without DryRun", loop.Name, loop.Name)

		return
	}
	if loop.TokenOriginNetwork == nil {
		plan.Note = "Asset = erc20 but TokenOriginNetwork is unset"

		return
	}

	sourceToken, err := o.resolveTokenOn(ctx, source, *loop.TokenOriginNetwork, tokenAddress)
	if err != nil {
		plan.Note = err.Error()

		return
	}
	plan.SourceTokenAddress = sourceToken

	if destination, ok := o.pool.network(hop.Destination); ok {
		destinationToken, err := o.resolveTokenOn(ctx, destination, *loop.TokenOriginNetwork, tokenAddress)
		if err != nil {
			plan.Note = err.Error()
		} else {
			plan.DestinationTokenAddress = destinationToken
		}
	}

	token, err := o.newToken(source.Client, sourceToken)
	if err != nil {
		plan.Note = err.Error()

		return
	}
	balance, err := token.BalanceOf(ctx, plan.From)
	if err != nil {
		plan.Note = fmt.Sprintf("read the ERC20 balance of %s on %s: %v", plan.From, source.Config.Name, err)

		return
	}
	plan.SourceBalance = balance
	plan.Fundable = balance.Cmp(plan.Amount) >= 0 &&
		plan.SourceNativeBalance.Cmp(source.Config.MinNativeReserve.BigInt()) > 0
	if !plan.Fundable {
		plan.Note = fmt.Sprintf("holds %s of token %s and %s wei of gas; the hop needs %s of the token and "+
			"more than %s wei of gas", balance, sourceToken, plan.SourceNativeBalance, plan.Amount,
			source.Config.MinNativeReserve.String())
	}
}

// resolveTokenOn returns the address a token has on one network: the origin address on its own
// origin network, and the bridge-created wrapped representation everywhere else.
func (o *Orchestrator) resolveTokenOn(
	ctx context.Context, network *pooledNetwork, originNetwork uint32, originToken common.Address,
) (common.Address, error) {
	if network.Config.NetworkID == originNetwork {
		return originToken, nil
	}

	wrapped, err := network.Bridge.GetTokenWrappedAddress(ctx, originNetwork, originToken)
	if err != nil {
		return common.Address{}, fmt.Errorf("resolve the wrapped address of token %s (origin network %d) "+
			"on %s: %w", originToken, originNetwork, network.Config.Name, err)
	}
	if wrapped == (common.Address{}) {
		return common.Address{}, fmt.Errorf("token %s (origin network %d) has no wrapped representation on "+
			"%s yet: it is created by the first claim of that token there", originToken, originNetwork,
			network.Config.Name)
	}

	return wrapped, nil
}

// DeployTokenRequest asks for a fresh test ERC20 on one network.
type DeployTokenRequest struct {
	// NetworkID is the network to deploy on. Required, must be configured.
	NetworkID uint32
	// Name and Symbol are the ERC20 metadata. Empty values are filled in from the tool's
	// defaults.
	Name   string
	Symbol string
	// Mint is how much to mint to the signing account after deployment. Nil or zero mints
	// nothing.
	Mint *big.Int
	// LoopName, when set, records the deployed token as that loop's token in the state file, so a
	// later `run` reuses it instead of deploying another one.
	LoopName string
}

// DeployTokenResult reports what DeployToken did.
type DeployTokenResult struct {
	// NetworkID and NetworkName say where the token lives.
	NetworkID   uint32         `json:"network_id"`
	NetworkName string         `json:"network_name"`
	Address     common.Address `json:"address"`
	Name        string         `json:"name"`
	Symbol      string         `json:"symbol"`
	// Owner is the account the token was deployed and minted from.
	Owner common.Address `json:"owner"`
	// Minted is how much was minted to Owner, and Balance its resulting balance.
	Minted  *big.Int `json:"minted,omitempty"`
	Balance *big.Int `json:"balance,omitempty"`
	// RecordedForLoop names the loop the address was persisted for, empty when it was not.
	RecordedForLoop string `json:"recorded_for_loop,omitempty"`
}

// DeployToken deploys the freely-mintable test ERC20 on one configured network, optionally mints to
// the signing account, and optionally records it as a loop's token so a later run reuses it rather
// than leaving a trail of abandoned contracts behind.
func (o *Orchestrator) DeployToken(
	ctx context.Context, req DeployTokenRequest,
) (*DeployTokenResult, error) {
	network, ok := o.pool.network(req.NetworkID)
	if !ok {
		return nil, fmt.Errorf("deploy token: network %d is not configured", req.NetworkID)
	}

	name, symbol := req.Name, req.Symbol
	if name == "" {
		name = tokenNamePrefix + network.Config.Name
	}
	if symbol == "" {
		symbol = tokenSymbol
	}

	token, err := o.deployToken(ctx, network.Client, name, symbol)
	if err != nil {
		return nil, fmt.Errorf("deploy token on network %d (%s): %w", req.NetworkID, network.Config.Name, err)
	}

	owner := network.Client.From()
	result := &DeployTokenResult{
		NetworkID:   req.NetworkID,
		NetworkName: network.Config.Name,
		Address:     token.Address(),
		Name:        name,
		Symbol:      symbol,
		Owner:       owner,
	}

	if req.Mint != nil && req.Mint.Sign() > 0 {
		if _, err := token.Mint(ctx, owner, req.Mint); err != nil {
			return result, fmt.Errorf("deploy token on network %d (%s): mint %s to %s: %w",
				req.NetworkID, network.Config.Name, req.Mint, owner, err)
		}
		result.Minted = new(big.Int).Set(req.Mint)
	}
	if balance, err := token.BalanceOf(ctx, owner); err == nil {
		result.Balance = balance
	}

	if req.LoopName != "" {
		record := &TokenState{
			LoopName:      req.LoopName,
			OriginNetwork: req.NetworkID,
			Address:       token.Address(),
			Name:          name,
			Symbol:        symbol,
			DeployedAt:    o.now().UTC(),
		}
		record.AddMinted(result.Minted)
		o.setTokenRecord(record)
		o.saveState(ctx)
		result.RecordedForLoop = req.LoopName
	}

	o.logger.Infof("bridge_loop_tester: deployed ERC20 %s (%s/%s) on network %d (%s) owner=%s minted=%s "+
		"recorded_for_loop=%q",
		result.Address, name, symbol, req.NetworkID, network.Config.Name, owner,
		bigIntString(result.Minted), result.RecordedForLoop)

	return result, nil
}

// RecoveryClaimRequest identifies one deposit to claim by hand: the network it was made on, the
// network it is claimable on, and its deposit count.
type RecoveryClaimRequest struct {
	// Source is the network the bridge transaction was submitted on.
	Source uint32
	// Destination is the network the deposit is claimable on.
	Destination uint32
	// DepositCount is the deposit's index in Source's exit tree (the BridgeEvent's depositCount).
	DepositCount uint32
	// GasLimit, when non-zero, overrides the claim transaction's gas estimate.
	GasLimit uint64
}

// RecoveryClaimResult reports what the recovery claim found and did.
type RecoveryClaimResult struct {
	// Source, Destination and DepositCount echo the request.
	Source       uint32 `json:"source"`
	Destination  uint32 `json:"destination"`
	DepositCount uint32 `json:"deposit_count"`
	// GlobalIndex is the deposit's global index, derived from Source and DepositCount.
	GlobalIndex *big.Int `json:"global_index"`
	// AlreadyClaimed is true when the destination bridge reported the deposit claimed before this
	// command submitted anything - in which case nothing was submitted.
	AlreadyClaimed bool `json:"already_claimed"`
	// LeafType says which entry point was used: 0 => claimAsset, 1 => claimMessage.
	LeafType uint8 `json:"leaf_type"`
	// L1InfoTreeIndex is the index the deposit was indexed at (I), and InjectedLeafIndex the
	// index actually injected on the destination (I'), which the proof was fetched for.
	L1InfoTreeIndex   uint32 `json:"l1_info_tree_index"`
	InjectedLeafIndex uint32 `json:"injected_leaf_index"`
	// OriginNetwork, OriginAddress, DestinationAddress and Amount are the deposit's own fields.
	OriginNetwork      uint32         `json:"origin_network"`
	OriginAddress      common.Address `json:"origin_address"`
	DestinationAddress common.Address `json:"destination_address"`
	Amount             *big.Int       `json:"amount"`
	// ClaimTxHash, ClaimBlockNumber and ClaimGasUsed describe the submitted claim.
	ClaimTxHash      common.Hash `json:"claim_tx_hash,omitempty"`
	ClaimBlockNumber uint64      `json:"claim_block_number,omitempty"`
	ClaimGasUsed     uint64      `json:"claim_gas_used,omitempty"`
	// Claimed is the destination bridge's isClaimed verdict after the submission.
	Claimed bool `json:"claimed"`
}

// Claim submits one recovery claim for a deposit this process did not make: the `claim` command's
// engine. It is the manual counterpart to a hop's claim step, for a deposit left unclaimed by a
// crashed run, a paused autoclaim service, or an operator's own bridge transaction.
//
// It never bridges, and it is safe to run twice: it reads the destination bridge's isClaimed first
// and returns AlreadyClaimed rather than submitting a transaction that would revert. It threads the
// actually-injected leaf index (I'), not the polled one (I), into the claim proof - the same
// requirement the hop engine implements, and the same silent-desync bug if it did not.
func (o *Orchestrator) Claim(ctx context.Context, req RecoveryClaimRequest) (*RecoveryClaimResult, error) {
	if _, ok := o.pool.network(req.Source); !ok {
		return nil, fmt.Errorf("claim: source network %d is not configured", req.Source)
	}
	destination, destinationConfigured := o.pool.network(req.Destination)
	if !destinationConfigured {
		return nil, fmt.Errorf("claim: destination network %d is not configured", req.Destination)
	}

	result := &RecoveryClaimResult{
		Source:       req.Source,
		Destination:  req.Destination,
		DepositCount: req.DepositCount,
		GlobalIndex:  GlobalIndex(req.Source, req.DepositCount),
	}

	claimed, err := destination.Bridge.IsClaimed(ctx, req.DepositCount, req.Source)
	if err != nil {
		return result, fmt.Errorf("claim: read isClaimed(%d, %d) on %s: %w",
			req.DepositCount, req.Source, destination.Config.Name, err)
	}
	if claimed {
		result.AlreadyClaimed = true
		result.Claimed = true
		o.logger.Infof("bridge_loop_tester: claim: deposit_count=%d from network %d is already claimed on "+
			"network %d (%s); nothing to do",
			req.DepositCount, req.Source, req.Destination, destination.Config.Name)

		return result, nil
	}

	deposit, err := o.proxy.BridgeByDepositCount(ctx, req.Source, req.DepositCount)
	if err != nil {
		return result, fmt.Errorf("claim: look up the deposit (network_id=%d deposit_count=%d) through the "+
			"proxy: %w", req.Source, req.DepositCount, err)
	}
	if deposit.DestinationNetwork != req.Destination {
		return result, fmt.Errorf("claim: deposit_count=%d on network %d is destined for network %d, not the "+
			"requested %d", req.DepositCount, req.Source, deposit.DestinationNetwork, req.Destination)
	}

	result.LeafType = deposit.LeafType
	result.OriginNetwork = deposit.OriginNetwork
	result.OriginAddress = common.HexToAddress(string(deposit.OriginAddress))
	result.DestinationAddress = common.HexToAddress(string(deposit.DestinationAddress))
	result.Amount = deposit.Amount.ToBigInt()

	proof, err := o.recoveryClaimProof(ctx, req, result)
	if err != nil {
		return result, err
	}

	request := ClaimRequest{
		ProofLocalExitRoot:  claimProofToBytes(proof.ProofLocalExitRoot),
		ProofRollupExitRoot: claimProofToBytes(proof.ProofRollupExitRoot),
		GlobalIndex:         result.GlobalIndex,
		MainnetExitRoot:     common.HexToHash(string(proof.L1InfoTreeLeaf.MainnetExitRoot)),
		RollupExitRoot:      common.HexToHash(string(proof.L1InfoTreeLeaf.RollupExitRoot)),
		OriginNetwork:       result.OriginNetwork,
		OriginAddress:       result.OriginAddress,
		DestinationNetwork:  req.Destination,
		DestinationAddress:  result.DestinationAddress,
		Amount:              result.Amount,
		Metadata:            common.FromHex(deposit.Metadata),
		GasLimit:            req.GasLimit,
	}

	receipt, err := o.submitRecoveryClaim(ctx, destination, result.LeafType, request)
	if err != nil {
		if IsAlreadyClaimed(err) {
			result.AlreadyClaimed = true
			result.Claimed = true
			o.logger.Warnf("bridge_loop_tester: claim: the submission reverted with AlreadyClaimed - "+
				"something claimed deposit_count=%d from network %d while this command was running",
				req.DepositCount, req.Source)

			return result, nil
		}

		return result, fmt.Errorf("claim: submit the claim on %s: %w", destination.Config.Name, err)
	}

	result.ClaimTxHash = receipt.TxHash
	result.ClaimGasUsed = receipt.GasUsed
	if receipt.BlockNumber != nil {
		result.ClaimBlockNumber = receipt.BlockNumber.Uint64()
	}
	result.Claimed, err = destination.Bridge.IsClaimed(ctx, req.DepositCount, req.Source)
	if err != nil {
		return result, fmt.Errorf("claim: re-read isClaimed(%d, %d) on %s after the claim: %w",
			req.DepositCount, req.Source, destination.Config.Name, err)
	}

	o.logger.Infof("bridge_loop_tester: claim submitted deposit_count=%d source=%d destination=%d "+
		"global_index=%s leaf_type=%d claim_tx=%s gas_used=%d claimed=%t",
		req.DepositCount, req.Source, req.Destination, result.GlobalIndex, result.LeafType,
		result.ClaimTxHash, result.ClaimGasUsed, result.Claimed)

	return result, nil
}

// recoveryClaimProof walks the three readiness gates for a recovery claim, bounded by HopTimeout,
// and records both leaf indices on the result.
func (o *Orchestrator) recoveryClaimProof(
	ctx context.Context, req RecoveryClaimRequest, result *RecoveryClaimResult,
) (*bridgeservicetypes.ClaimProof, error) {
	poll := o.cfg.Global.PollInterval.Duration
	budget := o.cfg.Global.HopTimeout.Duration

	leafIndex, err := o.proxy.WaitL1InfoTreeIndex(ctx, req.Source, uint64(req.DepositCount), poll, budget)
	if err != nil {
		return nil, fmt.Errorf("claim: wait for the L1 info tree index of deposit_count=%d on network %d: %w",
			req.DepositCount, req.Source, err)
	}
	result.L1InfoTreeIndex = leafIndex

	injected, err := o.proxy.WaitInjectedLeaf(ctx, req.Destination, leafIndex, poll, budget)
	if err != nil {
		return nil, fmt.Errorf("claim: wait for leaf %d to be injected on network %d: %w",
			leafIndex, req.Destination, err)
	}
	result.InjectedLeafIndex = injected

	// I', never I: the injected index may exceed the polled one, and proving against the wrong one
	// desyncs the claim's exit roots from what the destination really has.
	proof, err := o.proxy.WaitClaimProof(ctx, req.Source, injected, req.DepositCount, poll, budget)
	if err != nil {
		return nil, fmt.Errorf("claim: wait for the claim proof of deposit_count=%d at leaf %d on network "+
			"%d: %w", req.DepositCount, injected, req.Source, err)
	}

	return proof, nil
}

// submitRecoveryClaim picks claimAsset or claimMessage from the deposit's leaf type.
func (o *Orchestrator) submitRecoveryClaim(
	ctx context.Context, destination *pooledNetwork, leafType uint8, request ClaimRequest,
) (*ethtypes.Receipt, error) {
	switch leafType {
	case leafTypeAsset:
		return destination.Bridge.ClaimAsset(ctx, request)
	case leafTypeMessage:
		return destination.Bridge.ClaimMessage(ctx, request)
	default:
		return nil, fmt.Errorf("the deposit's leaf type %d is neither an asset (%d) nor a message (%d)",
			leafType, leafTypeAsset, leafTypeMessage)
	}
}

// StatusReport is the offline view of a persisted run: what every loop was doing when the state
// file was last written, and where its value sits. It reads no chain and no proxy, so it works
// while the environment it was testing is down - which is when it is most needed.
type StatusReport struct {
	// StatePath is the file the report was read from, empty when persistence is disabled.
	StatePath string `json:"state_path,omitempty"`
	// Exists is false when no state file has been written yet.
	Exists bool `json:"exists"`
	// UpdatedAt is when the state file was last written.
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	// Loops holds one entry per configured loop, in configuration order.
	Loops []LoopStatus `json:"loops"`
}

// LoopStatus is one loop's persisted position.
type LoopStatus struct {
	// Name, Asset, Amount, Route and Enabled describe the configured loop.
	Name    string    `json:"name"`
	Asset   AssetKind `json:"asset"`
	Amount  string    `json:"amount"`
	Route   []string  `json:"route"`
	Enabled bool      `json:"enabled"`
	// CyclesCompleted and CyclesAttempted are the persisted counters.
	CyclesCompleted uint64 `json:"cycles_completed"`
	CyclesAttempted uint64 `json:"cycles_attempted"`
	// InFlightState names the hop state the loop was in when the file was written, empty when no
	// hop was in flight.
	InFlightState HopState `json:"in_flight_state,omitempty"`
	// InFlightBridgeTx is the bridge transaction of that in-flight hop, if it had one.
	InFlightBridgeTx common.Hash `json:"in_flight_bridge_tx,omitempty"`
	// Halted, HaltClass and LastError record a permanent stop.
	Halted    bool         `json:"halted"`
	HaltClass FailureClass `json:"halt_class,omitempty"`
	LastError string       `json:"last_error,omitempty"`
	// ValueLocation says where the loop's value sits, and whether it is stranded.
	ValueLocation ValueLocation `json:"value_location"`
	// TokenAddress is the ERC20 the loop moves, zero for an ETH loop or one with no token yet.
	TokenAddress common.Address `json:"token_address,omitempty"`
	// WrappedTokens maps decimal network ID to the token's wrapped address there, as discovered.
	WrappedTokens map[string]common.Address `json:"wrapped_tokens,omitempty"`
}

// Status reads a config and its state file and reports where every loop stands, without touching
// the network. It is a package-level function rather than an Orchestrator method precisely because
// it must work when no RPC endpoint can be dialed.
func Status(ctx context.Context, cfg *Config, store StateStore) (*StatusReport, error) {
	if cfg == nil {
		return nil, fmt.Errorf("status: config is required")
	}

	resolved, err := resolveStateStore(store, cfg.Global.StatePath)
	if err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}

	state, err := resolved.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}

	report := &StatusReport{
		StatePath: cfg.Global.StatePath,
		Exists:    !state.UpdatedAt.IsZero(),
		UpdatedAt: state.UpdatedAt,
	}

	names := cfg.NetworksByID()
	for _, loop := range cfg.Loops {
		status := LoopStatus{
			Name:    loop.Name,
			Asset:   loop.Asset,
			Amount:  loop.Amount.String(),
			Route:   routeOf(loop),
			Enabled: loop.Enabled,
		}

		if record, ok := state.Loops[loop.Name]; ok {
			status.CyclesCompleted = record.CyclesCompleted
			status.CyclesAttempted = record.CyclesAttempted
			status.Halted = record.Halted
			status.HaltClass = record.HaltClass
			status.LastError = record.LastError
			if record.InFlight != nil {
				status.InFlightState = record.InFlight.State
				status.InFlightBridgeTx = record.InFlight.BridgeTxHash
			}
			status.ValueLocation = describeValueLocation(loop, record, names)
		} else {
			status.ValueLocation = describeValueLocation(loop, &LoopState{Name: loop.Name}, names)
		}
		if token := state.Token(loop.Name); token != nil {
			status.TokenAddress = token.Address
			status.WrappedTokens = token.Wrapped
		}

		report.Loops = append(report.Loops, status)
	}

	return report, nil
}

// describeValueLocation is Status's offline equivalent of (*Orchestrator).valueLocation: same rule
// (hop cursor 0 means at rest on the origin, anything else means stranded), computed from a
// persisted record and the configured network names alone.
func describeValueLocation(loop Loop, record *LoopState, names map[uint32]Network) ValueLocation {
	nameOf := func(networkID uint32) string {
		if network, ok := names[networkID]; ok {
			return network.Name
		}

		return unknownValue
	}

	origin := loop.Hops[0].Source
	if record.HopIndex <= 0 || record.HopIndex >= len(loop.Hops) {
		return ValueLocation{
			NetworkID:   origin,
			NetworkName: nameOf(origin),
			Detail: fmt.Sprintf("at rest on the loop's origin network %d (%s); the ring is closed",
				origin, nameOf(origin)),
		}
	}

	hop := loop.Hops[record.HopIndex]
	bridged := record.InFlight != nil && record.InFlight.BridgeTxHash != (common.Hash{})
	location := ValueLocation{
		NetworkID:   hop.Source,
		NetworkName: nameOf(hop.Source),
		Stranded:    true,
		InFlight:    bridged,
		HopIndex:    record.HopIndex,
	}
	if bridged {
		location.Detail = fmt.Sprintf("STRANDED in flight: hop %d (%d->%d) bridged in %s but was not "+
			"claimed on network %d (%s)", record.HopIndex, hop.Source, hop.Destination,
			record.InFlight.BridgeTxHash, hop.Destination, nameOf(hop.Destination))

		return location
	}
	location.Detail = fmt.Sprintf("STRANDED at rest on network %d (%s): hop %d (%d->%d) never bridged",
		hop.Source, nameOf(hop.Source), record.HopIndex, hop.Source, hop.Destination)

	return location
}
