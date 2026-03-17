# sync package

Provides the building blocks for EVM-based block synchronizers. A syncer tracks events emitted by one or more smart contracts, persists them atomically per block, and handles chain reorganisations automatically.

## Architecture overview

```
┌─────────────────────────────────────────────────────┐
│                      EVMDriver                      │
│  - main sync loop (Sync)                            │
│  - reorg detection & recovery                       │
│  - retry logic                                      │
│  - block subscriber pub/sub                         │
└────────────────┬──────────────────┬─────────────────┘
                 │                  │
      ┌──────────▼──────┐  ┌────────▼──────────┐
      │  EVMDownloader  │  │    processor      │
      │  (fetch blocks  │  │  (store blocks +  │
      │   + parse logs) │  │   events in DB)   │
      └──────────┬──────┘  └───────────────────┘
                 │
        ┌────────▼────────┐
        │  LogAppenderMap │
        │  (event topic → │
        │   handler func) │
        └─────────────────┘
```

## How to implement a new syncer

Three pieces are needed: an **Event struct**, a **`buildAppender` function**, and a **processor**.

### 1. Event struct

Define one struct per contract event you want to index. Use `any` as the type stored in `sync.Block.Events`.

```go
// transfer.go

// TransferEvent represents an ERC-20 Transfer event.
type TransferEvent struct {
    From   common.Address
    To     common.Address
    Amount *big.Int
}
```

### 2. `buildAppender` function

`buildAppender` returns a `sync.LogAppenderMap` — a map from event topic hash to a handler function. Each handler parses a raw `types.Log` and appends the decoded event to `b.Events`.

```go
// downloader.go

var transferEventSignature = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

func buildAppender(
    contractABI *abi.ABI,
) (sync.LogAppenderMap, error) {
    appender := make(sync.LogAppenderMap)

    appender[transferEventSignature] = func(b *sync.EVMBlock, l types.Log) error {
        var ev TransferEvent
        if err := contractABI.UnpackIntoInterface(&ev, "Transfer", l.Data); err != nil {
            return fmt.Errorf("buildAppender Transfer: unpack: %w", err)
        }
        // Indexed topics are not in Data; decode them from Topics.
        ev.From = common.BytesToAddress(l.Topics[1].Bytes())
        ev.To   = common.BytesToAddress(l.Topics[2].Bytes())
        b.Events = append(b.Events, ev)
        return nil
    }

    return appender, nil
}
```

> For events with indexed parameters: topic[0] is always the event signature; topic[1], topic[2], … are the indexed arguments in declaration order.

### 3. processor

The processor must implement the `processorInterface` used by `EVMDriver`:

```go
type processorInterface interface {
    GetLastProcessedBlock(ctx context.Context) (uint64, bool, error)
    ProcessBlock(ctx context.Context, block Block) error
    Reorg(ctx context.Context, firstReorgedBlock uint64) error
}
```

A typical implementation:

```go
// processor.go

type processor struct {
    storage MyStorager
    log     aggkitcommon.Logger
    timeout time.Duration
}

// GetLastProcessedBlock returns the highest block number stored in the DB.
// The bool indicates whether any block has been processed yet.
func (p *processor) GetLastProcessedBlock(ctx context.Context) (uint64, bool, error) {
    block, err := p.storage.GetLastBlock(ctx)
    if errors.Is(err, db.ErrNotFound) {
        return 0, false, nil
    }
    if err != nil {
        return 0, false, err
    }
    return block.Num, true, nil
}

// ProcessBlock stores the block and all its events atomically.
func (p *processor) ProcessBlock(ctx context.Context, block sync.Block) error {
    tx, err := p.storage.NewTx(ctx)
    if err != nil {
        return err
    }
    defer func() { /* rollback on error */ }()

    if err := p.storage.InsertBlock(ctx, tx, block.Num, block.Hash); err != nil {
        return err
    }
    for _, e := range block.Events {
        switch ev := e.(type) {
        case TransferEvent:
            if err := p.storage.InsertTransfer(ctx, tx, block.Num, ev); err != nil {
                return err
            }
        }
    }
    return tx.Commit()
}

// Reorg deletes all data for blocks >= firstReorgedBlock.
func (p *processor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
    tx, err := p.storage.NewTx(ctx)
    if err != nil {
        return err
    }
    defer func() { /* rollback on error */ }()
    if err := p.storage.DeleteBlocksFrom(ctx, tx, firstReorgedBlock); err != nil {
        return err
    }
    return tx.Commit()
}
```

### 4. Wiring it together

```go
// mysyncer.go

type MySync struct {
    driver    *sync.EVMDriver
    processor *processor
}

func NewMySync(
    ctx            context.Context,
    cfg            Config,
    rd             sync.ReorgDetector,
    ethClient      aggkittypes.EthClienter,
    syncerID       string,
) (*MySync, error) {
    store, err := storage.New(cfg.DBPath)
    if err != nil {
        return nil, err
    }

    proc := &processor{storage: store, timeout: cfg.DBQueryTimeout.Duration}

    contractABI, err := loadABI() // load your contract ABI
    if err != nil {
        return nil, err
    }

    appender, err := buildAppender(contractABI)
    if err != nil {
        return nil, err
    }

    rh := &sync.RetryHandler{
        MaxRetryAttemptsAfterError: cfg.MaxRetryAttemptsAfterError,
        RetryAfterErrorPeriod:      cfg.RetryAfterErrorPeriod.Duration,
    }

    downloader, err := sync.NewEVMDownloader(
        syncerID,
        sync.NewAdapterEthClientToMultidownloader(ethClient),
        cfg.SyncBlockChunkSize,
        cfg.BlockFinality,
        cfg.WaitForNewBlocksPeriod.Duration,
        appender,
        []common.Address{cfg.ContractAddr}, // contracts to filter events from
        rh,
        rd.GetFinalizedBlockType(),
        rd,
        syncerID,
    )
    if err != nil {
        return nil, err
    }

    compatibilityChecker := compatibility.NewCompatibilityCheck(
        cfg.RequireStorageContentCompatibility,
        downloader.RuntimeData,
        proc,
    )

    driver, err := sync.NewEVMDriver(rd, proc, downloader, syncerID, bufferSize, rh, compatibilityChecker)
    if err != nil {
        return nil, err
    }

    return &MySync{driver: driver, processor: proc}, nil
}

func (s *MySync) Start(ctx context.Context) {
    s.driver.Sync(ctx)
}
```

## Key types reference

| Type | Package | Description |
|---|---|---|
| `EVMDriver` | `sync` | Main sync loop, reorg handling, retry |
| `EVMDownloader` | `sync` | Downloads blocks and parses logs via `LogAppenderMap` |
| `LogAppenderMap` | `sync` | `map[topic hash → handler]` — decodes logs into events |
| `Block` | `sync` | Block number + hash + `[]any` events |
| `EVMBlock` | `sync` | Internal block used during download (before processing) |
| `RetryHandler` | `sync` | Configures retry behaviour (max attempts, backoff period) |
| `RuntimeData` | `sync` | Chain ID + watched addresses, used for DB compatibility checks |

## Reference implementation

See [claimsync](../claimsync/) for a complete real-world example:

| File | Role |
|---|---|
| [`downloader.go`](../claimsync/downloader.go) | Event structs, `buildAppender`, log handlers |
| [`processor.go`](../claimsync/processor.go) | `processorInterface` implementation |
| [`claimsync.go`](../claimsync/claimsync.go) | Wires everything together in `NewClaimSync` |
