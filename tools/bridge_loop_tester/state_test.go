package bridgelooptester_test

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"

	bridgelooptester "github.com/agglayer/aggkit/tools/bridge_loop_tester"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestFileStateStoreRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "state.json")
	store, err := bridgelooptester.NewFileStateStore(path)
	require.NoError(t, err)

	// A missing file is "nothing persisted yet", not an error: a first run needs no setup.
	loaded, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, bridgelooptester.StateVersion, loaded.Version)
	require.Empty(t, loaded.Loops)

	state := bridgelooptester.NewState()
	record := state.Loop("eth-ring")
	record.CyclesCompleted = 3
	record.CyclesAttempted = 4
	record.HopIndex = 1
	record.ValueNetwork = 2
	record.Halted = true
	record.HaltClass = bridgelooptester.FailureClaimMode
	record.LastError = "boom"
	record.InFlight = &bridgelooptester.HopCheckpoint{
		State:         bridgelooptester.HopStateBridging,
		BridgeTxHash:  common.HexToHash("0xabc"),
		BridgeTxNonce: 7,
		DepositCount:  42,
	}
	state.SetToken(&bridgelooptester.TokenState{
		LoopName:      "erc20-ring",
		OriginNetwork: 1,
		Address:       common.HexToAddress("0xdead"),
		Name:          "BridgeLoopTester erc20-ring",
		Symbol:        "BLT",
	})
	state.Token("erc20-ring").AddMinted(big.NewInt(1_000))
	state.RecordWrapped("erc20-ring", 2, common.HexToAddress("0xbeef"))

	require.NoError(t, store.Save(context.Background(), state))

	reloaded, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(3), reloaded.Loop("eth-ring").CyclesCompleted)
	require.Equal(t, uint64(4), reloaded.Loop("eth-ring").CyclesAttempted)
	require.Equal(t, 1, reloaded.Loop("eth-ring").HopIndex)
	require.Equal(t, uint32(2), reloaded.Loop("eth-ring").ValueNetwork)
	require.True(t, reloaded.Loop("eth-ring").Halted)
	require.Equal(t, bridgelooptester.FailureClaimMode, reloaded.Loop("eth-ring").HaltClass)
	require.Equal(t, "boom", reloaded.Loop("eth-ring").LastError)
	require.NotNil(t, reloaded.Loop("eth-ring").InFlight)
	require.Equal(t, bridgelooptester.HopStateBridging, reloaded.Loop("eth-ring").InFlight.State)
	require.Equal(t, uint64(7), reloaded.Loop("eth-ring").InFlight.BridgeTxNonce)
	require.Equal(t, common.HexToAddress("0xdead"), reloaded.Token("erc20-ring").Address)
	require.Equal(t, big.NewInt(1_000), reloaded.Token("erc20-ring").MintedAmount())
	require.Equal(t, common.HexToAddress("0xbeef"), reloaded.Token("erc20-ring").Wrapped["2"])
	require.False(t, reloaded.UpdatedAt.IsZero())
}

// TestFileStateStoreWritesAtomically pins the property S6B's resume safety rests on: the live file
// is only ever replaced whole. If Save wrote in place, a reader that caught it mid-write could see
// a truncated document - and the hop engine reads "state bridging with no bridge_tx_hash" as proof
// the transaction was never signed, so a torn write is a licence to double-bridge.
func TestFileStateStoreWritesAtomically(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	store, err := bridgelooptester.NewFileStateStore(path)
	require.NoError(t, err)

	first := bridgelooptester.NewState()
	first.Loop("ring").CyclesCompleted = 1
	require.NoError(t, store.Save(context.Background(), first))

	// Concurrent readers must never see anything but a complete, decodable document.
	stop := make(chan struct{})
	var readerWg sync.WaitGroup
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				// The target is never absent: rename is atomic, so the path always resolves.
				require.NoError(t, readErr)
				return
			}
			var decoded map[string]any
			require.NoError(t, json.Unmarshal(content, &decoded),
				"the state file must never be readable in a partially-written form")
		}
	}()

	var writerWg sync.WaitGroup
	for writer := 0; writer < 4; writer++ {
		writerWg.Add(1)
		go func(writer int) {
			defer writerWg.Done()
			for i := 0; i < 25; i++ {
				state := bridgelooptester.NewState()
				state.Loop("ring").CyclesCompleted = uint64(writer*100 + i)
				state.Loop("ring").LastError = string(make([]byte, 4096)) // large enough to need >1 write
				require.NoError(t, store.Save(context.Background(), state))
			}
		}(writer)
	}
	writerWg.Wait()
	close(stop)
	readerWg.Wait()

	// No temporary file is left behind, and the live file is still exactly one document.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "Save must leave no temporary files behind")
	require.Equal(t, "state.json", entries[0].Name())

	final, err := store.Load(context.Background())
	require.NoError(t, err)
	require.NotNil(t, final.Loop("ring"))
}

func TestFileStateStoreRefusesFutureSchemaVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version": 9999}`), 0o600))

	store, err := bridgelooptester.NewFileStateStore(path)
	require.NoError(t, err)

	_, err = store.Load(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "schema version 9999")
}

func TestNoopStateStore(t *testing.T) {
	t.Parallel()

	store := bridgelooptester.NoopStateStore{}
	state, err := store.Load(context.Background())
	require.NoError(t, err)
	require.NotNil(t, state)

	state.Loop("ring").CyclesCompleted = 5
	require.NoError(t, store.Save(context.Background(), state))

	reloaded, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Zero(t, reloaded.Loop("ring").CyclesCompleted)
}

func TestFileStateStoreRequiresPath(t *testing.T) {
	t.Parallel()

	_, err := bridgelooptester.NewFileStateStore("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "path is required")
}

func TestTokenStateMintedAmount(t *testing.T) {
	t.Parallel()

	var nilRecord *bridgelooptester.TokenState
	require.Equal(t, new(big.Int), nilRecord.MintedAmount())

	record := &bridgelooptester.TokenState{}
	require.Equal(t, new(big.Int), record.MintedAmount())
	record.AddMinted(big.NewInt(7))
	record.AddMinted(big.NewInt(5))
	require.Equal(t, big.NewInt(12), record.MintedAmount())

	record.Minted = "not-a-number"
	require.Equal(t, new(big.Int), record.MintedAmount())
}

func TestStateRecordWrappedIgnoresZeroAddress(t *testing.T) {
	t.Parallel()

	state := bridgelooptester.NewState()
	state.RecordWrapped("missing-loop", 1, common.HexToAddress("0x1"))
	require.Nil(t, state.Token("missing-loop"))

	state.SetToken(&bridgelooptester.TokenState{LoopName: "ring", OriginNetwork: 1})
	state.RecordWrapped("ring", 2, common.Address{})
	require.Empty(t, state.Token("ring").Wrapped)

	state.RecordWrapped("ring", 2, common.HexToAddress("0x2"))
	require.Equal(t, common.HexToAddress("0x2"), state.Token("ring").Wrapped["2"])
}

func TestFileStateStoreLoadEmptyFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	store, err := bridgelooptester.NewFileStateStore(path)
	require.NoError(t, err)

	state, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Empty(t, state.Loops)
	require.True(t, state.UpdatedAt.IsZero())
}
