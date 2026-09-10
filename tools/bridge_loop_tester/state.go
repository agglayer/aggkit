package bridgelooptester

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// StateVersion is the schema version stamped into every state file this package writes. Bump it
// whenever a field's meaning changes incompatibly; Load refuses a file from a future version
// rather than silently misreading it.
const StateVersion = 1

// stateFilePermissions is the mode of the state file and of the temporary file it is written
// through. It is deliberately owner-only: the file records transaction hashes and account
// positions of a run holding funded keys.
const stateFilePermissions = 0o600

// stateDirPermissions is the mode of the state file's parent directory, created on demand.
const stateDirPermissions = 0o700

// State is the whole persisted, resumable position of a run: which cycle each loop is on, which
// hop it stopped at, the checkpoint of the hop that was in flight, and the ERC20s it deployed.
//
// It is written as one JSON document, replaced atomically (see FileStateStore.Save). Atomicity is
// not a nicety here: the hop engine treats "checkpoint state bridging with no bridge_tx_hash" as
// proof that the bridge transaction was never signed, and therefore as licence to submit it. A
// torn write that produced exactly that byte pattern for a hop whose transaction *had* been signed
// would make the engine bridge a second time. Every writer of this file must therefore replace it
// whole or not at all.
type State struct {
	// Version is the schema version, always StateVersion when written by this package.
	Version int `json:"version"`
	// UpdatedAt is when this snapshot was written.
	UpdatedAt time.Time `json:"updated_at"`
	// ToolVersion records the build that wrote the snapshot, for post-mortem reading.
	ToolVersion string `json:"tool_version,omitempty"`
	// Loops holds one record per loop, keyed by Loop.Name.
	Loops map[string]*LoopState `json:"loops,omitempty"`
	// Tokens holds one record per ERC20 loop's deployed token, keyed by Loop.Name.
	Tokens map[string]*TokenState `json:"tokens,omitempty"`
}

// LoopState is one loop's persisted cursor.
//
// HopIndex is the resume cursor, and it is what makes a stranded ring recoverable: because a route
// is circular, a cycle that fails at hop i has left the value sitting on hop i's *source* network
// (if the bridge never happened) or in flight towards its destination (if it did). Restarting such
// a loop at hop 0 would try to spend value that is not there. So the cursor is only reset to 0
// when a cycle closes the ring, and the next cycle of a stranded loop continues at HopIndex.
type LoopState struct {
	// Name is the loop's configured name (the map key, repeated for standalone readability).
	Name string `json:"name"`
	// CyclesCompleted counts rings closed end to end since this state file was created.
	CyclesCompleted uint64 `json:"cycles_completed"`
	// CyclesAttempted counts ring passes started, completed or not. Iterations bounds this.
	CyclesAttempted uint64 `json:"cycles_attempted"`
	// HopIndex is the 0-based hop the next cycle resumes at: 0 for a healthy loop resting at its
	// origin, i > 0 for a loop whose value is stranded part-way around the ring.
	HopIndex int `json:"hop_index"`
	// ValueNetwork is the network the loop's value is believed to sit on right now: the origin
	// when the ring is closed, hop HopIndex's source when it is stranded.
	ValueNetwork uint32 `json:"value_network"`
	// InFlight is the checkpoint of the hop that was executing when this snapshot was written,
	// or nil when no hop was in flight. It is what a restart hands back to HopRequest.Resume.
	InFlight *HopCheckpoint `json:"in_flight,omitempty"`
	// Halted records that this loop stopped for a non-retryable reason and will not be re-driven
	// until an operator resets it (see FailureClass and the failure policy on Orchestrator).
	Halted bool `json:"halted"`
	// HaltClass names why it halted, empty when Halted is false.
	HaltClass FailureClass `json:"halt_class,omitempty"`
	// LastError is the message of the most recent hop failure, kept for `status` output.
	LastError string `json:"last_error,omitempty"`
	// UpdatedAt is when this loop's record last changed.
	UpdatedAt time.Time `json:"updated_at"`
}

// TokenState is the persisted lifecycle of one ERC20 loop's token: deployed once on the loop's
// TokenOriginNetwork, minted once, and reused on every restart instead of leaving a trail of
// abandoned contracts behind.
type TokenState struct {
	// LoopName is the loop the token belongs to (the map key, repeated for readability).
	LoopName string `json:"loop_name"`
	// OriginNetwork is the network the token contract lives on.
	OriginNetwork uint32 `json:"origin_network"`
	// Address is the token contract's address on OriginNetwork.
	Address common.Address `json:"address"`
	// Name and Symbol are the ERC20 metadata the tool deployed it with.
	Name   string `json:"name,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	// DeployTxHash is the deployment transaction, for traceability.
	DeployTxHash common.Hash `json:"deploy_tx_hash,omitempty"`
	// DeployedAt is when the tool deployed it.
	DeployedAt time.Time `json:"deployed_at,omitempty"`
	// Minted is the cumulative amount the tool has minted to its own account, as a decimal
	// string (wei-scale values exceed float64's exact range, so never a JSON number).
	Minted string `json:"minted,omitempty"`
	// Wrapped records the bridge-created wrapped representations discovered on the other
	// networks, keyed by decimal network ID. Purely informational - the tool always re-derives
	// them from the bridge - but it turns a "where did my balance go" question into a lookup.
	Wrapped map[string]common.Address `json:"wrapped,omitempty"`
}

// NewState returns an empty, initialized State stamped with the current schema version.
func NewState() *State {
	return &State{
		Version: StateVersion,
		Loops:   map[string]*LoopState{},
		Tokens:  map[string]*TokenState{},
	}
}

// Loop returns the record for the named loop, creating an empty one if it does not exist yet.
func (s *State) Loop(name string) *LoopState {
	if s.Loops == nil {
		s.Loops = map[string]*LoopState{}
	}
	record, ok := s.Loops[name]
	if !ok {
		record = &LoopState{Name: name}
		s.Loops[name] = record
	}

	return record
}

// Token returns the token record for the named loop, or nil when the loop has none yet.
func (s *State) Token(loopName string) *TokenState {
	if s.Tokens == nil {
		return nil
	}

	return s.Tokens[loopName]
}

// SetToken records a loop's token, replacing any previous record.
func (s *State) SetToken(record *TokenState) {
	if s.Tokens == nil {
		s.Tokens = map[string]*TokenState{}
	}
	s.Tokens[record.LoopName] = record
}

// RecordWrapped notes the wrapped representation of a loop's token on another network. It is a
// no-op when the loop has no token record or the address is zero.
func (s *State) RecordWrapped(loopName string, networkID uint32, address common.Address) {
	record := s.Token(loopName)
	if record == nil || address == (common.Address{}) {
		return
	}
	if record.Wrapped == nil {
		record.Wrapped = map[string]common.Address{}
	}
	record.Wrapped[strconv.FormatUint(uint64(networkID), 10)] = address
}

// MintedAmount returns the cumulative minted amount as a big.Int, or zero when unset/unparseable.
func (t *TokenState) MintedAmount() *big.Int {
	if t == nil || t.Minted == "" {
		return new(big.Int)
	}
	amount, ok := new(big.Int).SetString(t.Minted, decimalBase)
	if !ok {
		return new(big.Int)
	}

	return amount
}

// AddMinted adds amount to the cumulative minted total.
func (t *TokenState) AddMinted(amount *big.Int) {
	if t == nil || amount == nil {
		return
	}
	t.Minted = new(big.Int).Add(t.MintedAmount(), amount).String()
}

// StateStore persists and restores a State. It exists as an interface so a run can be driven
// without a state file at all (NoopStateStore), against a file (FileStateStore), or against an
// in-memory store in a test.
//
// Implementations must be safe for concurrent use: every loop goroutine persists checkpoints
// through the same store.
type StateStore interface {
	// Load returns the persisted state, or a fresh empty State when nothing is persisted yet.
	Load(ctx context.Context) (*State, error)
	// Save replaces the persisted state with state, atomically: a reader (including this tool
	// after a crash) sees either the previous snapshot or this one, never a mixture.
	Save(ctx context.Context, state *State) error
}

// NoopStateStore discards every Save and always Loads an empty State. It is what a run with an
// empty Global.StatePath uses: such a run is still resumable *within* the process (the orchestrator
// keeps the in-memory State), just not across restarts.
type NoopStateStore struct{}

var _ StateStore = NoopStateStore{}

// Load implements StateStore, always returning a fresh empty State.
func (NoopStateStore) Load(context.Context) (*State, error) { return NewState(), nil }

// Save implements StateStore, discarding state.
func (NoopStateStore) Save(context.Context, *State) error { return nil }

// FileStateStore persists State as one JSON document at Path.
//
// # Atomicity
//
// Save never writes into the live file. It writes the complete document to a temporary file in the
// *same directory* (so the rename cannot cross a filesystem boundary and degrade into a copy),
// fsyncs that file, closes it, renames it over the target - which is atomic on POSIX - and finally
// fsyncs the parent directory so the rename itself is durable, not just the bytes. A crash at any
// point leaves either the previous complete snapshot or the new complete snapshot on disk.
//
// This is what the hop engine's resume contract rests on: it reads "checkpoint state bridging with
// no bridge_tx_hash" as proof that the bridge transaction was never signed. A partially-written
// file that happened to end mid-object could present that pattern for a hop whose transaction was
// already signed and broadcast, and the engine would then bridge the same value twice.
type FileStateStore struct {
	// Path is the state file. Its parent directory is created on demand.
	Path string

	// mu serializes Save calls (and their temp files) from concurrent loop goroutines.
	mu sync.Mutex
}

var _ StateStore = (*FileStateStore)(nil)

// NewFileStateStore returns a FileStateStore for path.
func NewFileStateStore(path string) (*FileStateStore, error) {
	if path == "" {
		return nil, fmt.Errorf("new file state store: path is required")
	}

	return &FileStateStore{Path: path}, nil
}

// Load reads and decodes the state file. A missing file is not an error: it means "nothing
// persisted yet" and yields a fresh empty State, so a first run needs no setup. A file written by
// a newer schema version is refused rather than misread.
func (f *FileStateStore) Load(_ context.Context) (*State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	content, err := os.ReadFile(f.Path)
	if os.IsNotExist(err) {
		return NewState(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("load state from %s: %w", f.Path, err)
	}
	if len(content) == 0 {
		return NewState(), nil
	}

	var state State
	if err := json.Unmarshal(content, &state); err != nil {
		return nil, fmt.Errorf("load state from %s: decode: %w", f.Path, err)
	}
	if state.Version > StateVersion {
		return nil, fmt.Errorf("load state from %s: file is schema version %d but this build only "+
			"understands up to %d", f.Path, state.Version, StateVersion)
	}
	if state.Loops == nil {
		state.Loops = map[string]*LoopState{}
	}
	if state.Tokens == nil {
		state.Tokens = map[string]*TokenState{}
	}
	state.Version = StateVersion

	return &state, nil
}

// Save writes state to Path atomically. See FileStateStore's doc comment for why the temp-file +
// rename + directory-fsync sequence matters rather than being a stylistic preference.
func (f *FileStateStore) Save(_ context.Context, state *State) error {
	if state == nil {
		return fmt.Errorf("save state to %s: state is required", f.Path)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	state.Version = StateVersion
	state.UpdatedAt = time.Now().UTC()

	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("save state to %s: encode: %w", f.Path, err)
	}
	content = append(content, '\n')

	dir := filepath.Dir(f.Path)
	if err := os.MkdirAll(dir, stateDirPermissions); err != nil {
		return fmt.Errorf("save state to %s: create directory %s: %w", f.Path, dir, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(f.Path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("save state to %s: create temporary file in %s: %w", f.Path, dir, err)
	}
	tmpName := tmp.Name()

	if err := writeAndSync(tmp, content); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("save state to %s: write %s: %w", f.Path, tmpName, err)
	}
	if err := os.Chmod(tmpName, stateFilePermissions); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("save state to %s: chmod %s: %w", f.Path, tmpName, err)
	}
	if err := os.Rename(tmpName, f.Path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("save state to %s: rename %s: %w", f.Path, tmpName, err)
	}

	// Fsync the directory so the rename itself survives a power loss, not only the bytes of the
	// file it points at. A failure here is reported but not fatal: the rename already happened, so
	// the on-disk state is consistent either way - only its durability against an immediate host
	// crash is weaker, and on some filesystems (and on Windows) directories cannot be synced at
	// all.
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("save state to %s: sync directory %s: %w", f.Path, dir, err)
	}

	return nil
}

// writeAndSync writes content to file, flushes it to the storage device and closes it.
func writeAndSync(file *os.File, content []byte) error {
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}

	return file.Close()
}

// syncDir fsyncs a directory so a rename inside it is durable.
func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := handle.Sync(); err != nil {
		_ = handle.Close()
		return err
	}

	return handle.Close()
}
