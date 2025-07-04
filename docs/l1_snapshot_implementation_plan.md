# L1 Snapshot Implementation Plan

## Implementation Questions and Decisions

### 1. Snapshot Scope and Components

**Question 1.1**: Which L1 components should be included in snapshots?

**Options**:
- [ ] All L1 components (l1infotreesync, bridgesync, lastgersync, reorgdetector)
- [ ] Only critical components (l1infotreesync, bridgesync)
- [ ] Configurable selection per user

**Recommendation**: Start with all L1 components for completeness, but make it configurable.

**Question 1.2**: Should we include L2 components in the same snapshot?

**Options**:
- [ ] Separate L1 and L2 snapshots
- [ ] Combined L1+L2 snapshots
- [ ] Modular snapshots (user chooses components)

**Recommendation**: Keep L1 and L2 separate initially for simplicity.

### 2. Snapshot Creation Strategy

**Question 2.1**: When should snapshots be created?

**Options**:
- [ ] Fixed time intervals (e.g., every 24 hours)
- [ ] Block height milestones (e.g., every 10,000 blocks)
- [ ] Hybrid approach (time + block height)
- [ ] On-demand creation

**Recommendation**: Hybrid approach - create snapshots every 24 hours OR when block height increases by 10,000, whichever comes first.

**Question 2.2**: How should we handle snapshot creation during active syncing?

**Options**:
- [ ] Pause syncing during snapshot creation
- [ ] Create snapshots from running databases (with potential inconsistency)
- [ ] Use database snapshots/backups
- [ ] Create snapshots from read replicas

**Recommendation**: Pause syncing during snapshot creation for consistency.

### 3. Storage and Distribution

**Question 3.1**: Where should snapshots be stored?

**Options**:
- [ ] GitHub Releases only
- [ ] GitHub Releases + CDN (AWS S3, Cloudflare R2)
- [ ] GitHub Releases + CDN + IPFS
- [ ] Custom storage solution

**Recommendation**: Start with GitHub Releases + CDN for reliability and performance.

**Question 3.2**: How should we handle snapshot versioning?

**Options**:
- [ ] Semantic versioning (v1.0.0, v1.1.0)
- [ ] Block height based (snapshot-15000000)
- [ ] Timestamp based (snapshot-20240115)
- [ ] Hybrid (v1.0.0-block-15000000)

**Recommendation**: Hybrid approach for clarity and traceability.

### 4. Validation and Security

**Question 4.1**: What level of validation should be performed?

**Options**:
- [ ] Basic checksums only
- [ ] Checksums + database integrity
- [ ] Checksums + database integrity + merkle tree validation
- [ ] Full validation including on-chain verification

**Recommendation**: Start with checksums + database integrity + merkle tree validation.

**Question 4.2**: How should we handle snapshot authenticity?

**Options**:
- [ ] Checksums only
- [ ] GPG signatures
- [ ] Checksums + GPG signatures
- [ ] Blockchain-based verification

**Recommendation**: Checksums + GPG signatures for security.

### 5. CLI Integration

**Question 5.1**: How should snapshot commands be integrated?

**Options**:
- [ ] Separate `snapshot` command group
- [ ] Flags on existing `run` command
- [ ] Both approaches
- [ ] New `sync` command group

**Recommendation**: Separate `snapshot` command group for clarity.

**Question 5.2**: Should snapshot download be automatic or manual?

**Options**:
- [ ] Always manual (user must run command)
- [ ] Automatic on first run if no databases exist
- [ ] Configurable automatic/manual
- [ ] Prompt user for choice

**Recommendation**: Configurable with automatic as default for better UX.

### 6. Performance and Size Optimization

**Question 6.1**: How should we handle large snapshot files?

**Options**:
- [ ] Single compressed archive
- [ ] Split into multiple files
- [ ] Incremental snapshots
- [ ] Streaming download

**Recommendation**: Start with single compressed archive, add incremental later.

**Question 6.2**: What compression should we use?

**Options**:
- [ ] gzip (fast, widely supported)
- [ ] bzip2 (better compression)
- [ ] xz (best compression)
- [ ] zstd (good balance)

**Recommendation**: gzip for wide compatibility, zstd as alternative.

## Technical Implementation Details

### Phase 1: Core Infrastructure

#### 1.1 Snapshot Service Interface

```go
// snapshot/types.go
package snapshot

import (
    "context"
    "time"
)

type SnapshotMetadata struct {
    Version     string                 `json:"version"`
    CreatedAt   time.Time              `json:"created_at"`
    BlockHeight uint64                 `json:"block_height"`
    BlockHash   string                 `json:"block_hash"`
    Components  map[string]Component   `json:"components"`
    Checksums   Checksums              `json:"checksums"`
    SizeBytes   int64                  `json:"size_bytes"`
    Compression string                 `json:"compression"`
}

type Component struct {
    LastProcessedBlock uint64 `json:"last_processed_block"`
    DatabasePath       string `json:"database_path"`
    // Component-specific fields
    L1InfoTreeRoot     string `json:"l1_info_tree_root,omitempty"`
    RollupExitTreeRoot string `json:"rollup_exit_tree_root,omitempty"`
    LeafCount          uint32 `json:"leaf_count,omitempty"`
    BridgeCount        uint32 `json:"bridge_count,omitempty"`
    ClaimCount         uint32 `json:"claim_count,omitempty"`
    GERCount           uint32 `json:"ger_count,omitempty"`
    TrackedBlocks      uint32 `json:"tracked_blocks,omitempty"`
}

type Checksums struct {
    SHA256 string `json:"sha256"`
    SHA512 string `json:"sha512"`
}

type SnapshotService interface {
    CreateSnapshot(ctx context.Context) (*SnapshotMetadata, error)
    ListSnapshots(ctx context.Context) ([]SnapshotMetadata, error)
    GetSnapshot(ctx context.Context, blockHeight uint64) (*SnapshotMetadata, error)
}

type SnapshotClient interface {
    DownloadSnapshot(ctx context.Context, metadata *SnapshotMetadata, outputDir string) error
    VerifySnapshot(ctx context.Context, filePath string) error
}
```

#### 1.2 Snapshot Creation Implementation

```go
// snapshot/creator.go
package snapshot

import (
    "context"
    "database/sql"
    "fmt"
    "os"
    "path/filepath"
    "time"

    "github.com/agglayer/aggkit/l1infotreesync"
    "github.com/agglayer/aggkit/bridgesync"
    "github.com/agglayer/aggkit/lastgersync"
    "github.com/agglayer/aggkit/reorgdetector"
)

type Creator struct {
    config     CreatorConfig
    logger     *log.Logger
    components map[string]ComponentInfo
}

type CreatorConfig struct {
    OutputDir       string
    CompressionLevel int
    IncludeComponents []string
}

type ComponentInfo struct {
    Name        string
    DBPath      string
    Validator   func(*sql.DB) error
}

func NewCreator(config CreatorConfig) *Creator {
    return &Creator{
        config: config,
        components: map[string]ComponentInfo{
            "l1infotreesync": {
                Name:   "L1InfoTreeSync",
                DBPath: "L1InfoTreeSync.sqlite",
                Validator: validateL1InfoTreeSync,
            },
            "bridgesync_l1": {
                Name:   "BridgeSyncL1",
                DBPath: "BridgeSyncL1.sqlite",
                Validator: validateBridgeSync,
            },
            "lastgersync": {
                Name:   "LastGERSync",
                DBPath: "LastGERSync.sqlite",
                Validator: validateLastGERSync,
            },
            "reorgdetector_l1": {
                Name:   "ReorgDetectorL1",
                DBPath: "ReorgDetectorL1.sqlite",
                Validator: validateReorgDetector,
            },
        },
    }
}

func (c *Creator) CreateSnapshot(ctx context.Context) (*SnapshotMetadata, error) {
    // 1. Stop L1 sync components
    if err := c.stopComponents(ctx); err != nil {
        return nil, fmt.Errorf("failed to stop components: %w", err)
    }
    defer c.startComponents(ctx)

    // 2. Create temporary directory for snapshot
    tempDir, err := os.MkdirTemp("", "snapshot-*")
    if err != nil {
        return nil, fmt.Errorf("failed to create temp dir: %w", err)
    }
    defer os.RemoveAll(tempDir)

    // 3. Copy databases
    components := make(map[string]Component)
    for name, info := range c.components {
        if !c.shouldIncludeComponent(name) {
            continue
        }

        component, err := c.copyDatabase(name, info, tempDir)
        if err != nil {
            return nil, fmt.Errorf("failed to copy %s: %w", name, err)
        }
        components[name] = component
    }

    // 4. Generate metadata
    metadata := &SnapshotMetadata{
        Version:     "1.0.0",
        CreatedAt:   time.Now(),
        BlockHeight: c.getCommonBlockHeight(components),
        BlockHash:   c.getBlockHash(components),
        Components:  components,
    }

    // 5. Create archive
    archivePath, err := c.createArchive(tempDir, metadata)
    if err != nil {
        return nil, fmt.Errorf("failed to create archive: %w", err)
    }

    // 6. Calculate checksums
    checksums, err := c.calculateChecksums(archivePath)
    if err != nil {
        return nil, fmt.Errorf("failed to calculate checksums: %w", err)
    }
    metadata.Checksums = checksums

    // 7. Get file size
    fileInfo, err := os.Stat(archivePath)
    if err != nil {
        return nil, fmt.Errorf("failed to get file info: %w", err)
    }
    metadata.SizeBytes = fileInfo.Size()

    return metadata, nil
}
```

#### 1.3 Snapshot Download Implementation

```go
// snapshot/downloader.go
package snapshot

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "time"
)

type Downloader struct {
    config     DownloaderConfig
    logger     *log.Logger
    storage    SnapshotStorage
}

type DownloaderConfig struct {
    DownloadTimeout time.Duration
    RetryAttempts   int
    RetryDelay      time.Duration
    VerifyChecksums bool
    VerifyData      bool
}

func (d *Downloader) DownloadSnapshot(ctx context.Context, metadata *SnapshotMetadata, outputDir string) error {
    // 1. Create output directory
    if err := os.MkdirAll(outputDir, 0755); err != nil {
        return fmt.Errorf("failed to create output dir: %w", err)
    }

    // 2. Download snapshot file
    snapshotPath := filepath.Join(outputDir, fmt.Sprintf("snapshot-%d.tar.gz", metadata.BlockHeight))
    if err := d.downloadFile(ctx, metadata.DownloadURL, snapshotPath); err != nil {
        return fmt.Errorf("failed to download snapshot: %w", err)
    }

    // 3. Verify checksums
    if d.config.VerifyChecksums {
        if err := d.verifyChecksums(snapshotPath, metadata.Checksums); err != nil {
            return fmt.Errorf("checksum verification failed: %w", err)
        }
    }

    // 4. Extract archive
    if err := d.extractArchive(snapshotPath, outputDir); err != nil {
        return fmt.Errorf("failed to extract archive: %w", err)
    }

    // 5. Verify database integrity
    if d.config.VerifyData {
        if err := d.verifyDatabases(outputDir, metadata); err != nil {
            return fmt.Errorf("database verification failed: %w", err)
        }
    }

    // 6. Replace existing databases
    if err := d.replaceDatabases(outputDir); err != nil {
        return fmt.Errorf("failed to replace databases: %w", err)
    }

    return nil
}

func (d *Downloader) downloadFile(ctx context.Context, url, filepath string) error {
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return err
    }

    client := &http.Client{Timeout: d.config.DownloadTimeout}
    resp, err := client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("download failed with status: %d", resp.StatusCode)
    }

    file, err := os.Create(filepath)
    if err != nil {
        return err
    }
    defer file.Close()

    _, err = io.Copy(file, resp.Body)
    return err
}
```

### Phase 2: CLI Integration

#### 2.1 CLI Commands Structure

```go
// cmd/snapshot.go
package main

import (
    "context"
    "fmt"
    "os"
    "path/filepath"

    "github.com/urfave/cli/v2"
    "github.com/agglayer/aggkit/snapshot"
)

func snapshotCommands() []*cli.Command {
    return []*cli.Command{
        {
            Name:  "snapshot",
            Usage: "Manage L1 snapshots",
            Subcommands: []*cli.Command{
                downloadSnapshotCmd(),
                listSnapshotsCmd(),
                verifySnapshotCmd(),
                createSnapshotCmd(),
            },
        },
    }
}

func downloadSnapshotCmd() *cli.Command {
    return &cli.Command{
        Name:  "download",
        Usage: "Download and apply L1 snapshot",
        Flags: []cli.Flag{
            &cli.Uint64Flag{
                Name:  "block-height",
                Usage: "Specific block height to download",
            },
            &cli.StringFlag{
                Name:  "output-dir",
                Usage: "Output directory for databases",
                Value: "./data",
            },
            &cli.BoolFlag{
                Name:  "verify",
                Usage: "Verify snapshot integrity",
                Value: true,
            },
            &cli.BoolFlag{
                Name:  "force",
                Usage: "Force download even if databases exist",
            },
        },
        Action: func(c *cli.Context) error {
            return downloadSnapshot(c)
        },
    }
}

func downloadSnapshot(c *cli.Context) error {
    ctx := c.Context
    blockHeight := c.Uint64("block-height")
    outputDir := c.String("output-dir")
    verify := c.Bool("verify")
    force := c.Bool("force")

    // Check if databases already exist
    if !force && databasesExist(outputDir) {
        return fmt.Errorf("databases already exist in %s. Use --force to overwrite", outputDir)
    }

    // Initialize snapshot client
    client := snapshot.NewClient(snapshot.DownloaderConfig{
        DownloadTimeout: 30 * time.Minute,
        RetryAttempts:   3,
        RetryDelay:      5 * time.Second,
        VerifyChecksums: verify,
        VerifyData:      verify,
    })

    // Get snapshot metadata
    var metadata *snapshot.SnapshotMetadata
    var err error

    if blockHeight > 0 {
        metadata, err = client.GetSnapshot(ctx, blockHeight)
    } else {
        metadata, err = client.GetLatestSnapshot(ctx)
    }
    if err != nil {
        return fmt.Errorf("failed to get snapshot metadata: %w", err)
    }

    // Download and apply snapshot
    if err := client.DownloadSnapshot(ctx, metadata, outputDir); err != nil {
        return fmt.Errorf("failed to download snapshot: %w", err)
    }

    fmt.Printf("Successfully downloaded and applied snapshot for block %d\n", metadata.BlockHeight)
    return nil
}
```

### Phase 3: Validation and Security

#### 3.1 Database Validation

```go
// snapshot/validator.go
package snapshot

import (
    "database/sql"
    "fmt"
    "path/filepath"

    "github.com/agglayer/aggkit/tree"
    "github.com/ethereum/go-ethereum/common"
)

type Validator struct {
    logger *log.Logger
}

func (v *Validator) ValidateDatabases(outputDir string, metadata *SnapshotMetadata) error {
    for name, component := range metadata.Components {
        dbPath := filepath.Join(outputDir, component.DatabasePath)

        // Open database
        db, err := sql.Open("sqlite3", dbPath)
        if err != nil {
            return fmt.Errorf("failed to open %s database: %w", name, err)
        }
        defer db.Close()

        // Check database integrity
        if err := v.checkDatabaseIntegrity(db); err != nil {
            return fmt.Errorf("database integrity check failed for %s: %w", name, err)
        }

        // Component-specific validation
        if err := v.validateComponent(name, db, component); err != nil {
            return fmt.Errorf("component validation failed for %s: %w", name, err)
        }
    }

    return nil
}

func (v *Validator) validateComponent(name string, db *sql.DB, component Component) error {
    switch name {
    case "l1infotreesync":
        return v.validateL1InfoTreeSync(db, component)
    case "bridgesync_l1":
        return v.validateBridgeSync(db, component)
    case "lastgersync":
        return v.validateLastGERSync(db, component)
    case "reorgdetector_l1":
        return v.validateReorgDetector(db, component)
    default:
        return fmt.Errorf("unknown component: %s", name)
    }
}

func (v *Validator) validateL1InfoTreeSync(db *sql.DB, component Component) error {
    // Validate L1 Info Tree
    if component.L1InfoTreeRoot != "" {
        expectedRoot := common.HexToHash(component.L1InfoTreeRoot)
        tree := tree.NewAppendOnlyTree(db, "l1info")

        lastRoot, err := tree.GetLastRoot(db)
        if err != nil {
            return fmt.Errorf("failed to get last root: %w", err)
        }

        if lastRoot.Hash != expectedRoot {
            return fmt.Errorf("L1 info tree root mismatch: expected %s, got %s",
                             expectedRoot, lastRoot.Hash)
        }
    }

    // Validate Rollup Exit Tree
    if component.RollupExitTreeRoot != "" {
        expectedRoot := common.HexToHash(component.RollupExitTreeRoot)
        tree := tree.NewUpdatableTree(db, "rollup_exit")

        lastRoot, err := tree.GetLastRoot(db)
        if err != nil {
            return fmt.Errorf("failed to get last rollup exit root: %w", err)
        }

        if lastRoot.Hash != expectedRoot {
            return fmt.Errorf("rollup exit tree root mismatch: expected %s, got %s",
                             expectedRoot, lastRoot.Hash)
        }
    }

    return nil
}
```

## Implementation Timeline

### Week 1-2: Core Infrastructure
- [ ] Implement snapshot types and interfaces
- [ ] Create snapshot creation service
- [ ] Implement basic database copying
- [ ] Add metadata generation

### Week 3-4: Download and Storage
- [ ] Implement snapshot download client
- [ ] Add GitHub Releases integration
- [ ] Implement checksum calculation and verification
- [ ] Add archive creation and extraction

### Week 5-6: CLI Integration
- [ ] Add snapshot CLI commands
- [ ] Integrate with existing run command
- [ ] Add configuration options
- [ ] Implement error handling and user feedback

### Week 7-8: Validation and Security
- [ ] Implement database integrity checks
- [ ] Add merkle tree validation
- [ ] Implement GPG signature verification
- [ ] Add comprehensive testing

### Week 9-10: Testing and Documentation
- [ ] Create comprehensive test suite
- [ ] Test with real data
- [ ] Update documentation
- [ ] Performance optimization

### Week 11-12: Deployment and Monitoring
- [ ] Set up automated snapshot creation
- [ ] Deploy to testnet
- [ ] Add monitoring and metrics
- [ ] Community testing and feedback

## Risk Assessment

### High Risk
- **Data Corruption**: Snapshot creation during active syncing could lead to inconsistent data
- **Security**: Malicious snapshots could compromise node security
- **Performance**: Large snapshot files could impact download performance

### Medium Risk
- **Storage Costs**: Regular snapshots could become expensive
- **Compatibility**: Schema changes could break snapshot compatibility
- **Network Issues**: Download failures could prevent node startup

### Low Risk
- **User Adoption**: Users might prefer manual syncing
- **Maintenance**: Ongoing snapshot maintenance overhead

## Mitigation Strategies

1. **Data Corruption**: Implement proper locking during snapshot creation
2. **Security**: Use cryptographic signatures and on-chain verification
3. **Performance**: Implement resumable downloads and compression
4. **Storage Costs**: Implement snapshot rotation and compression
5. **Compatibility**: Version snapshots and provide migration tools
6. **Network Issues**: Implement multiple download sources and retry logic

## Success Metrics

1. **Reduced Sync Time**: New nodes should sync in <30 minutes instead of hours/days
2. **Success Rate**: >95% of snapshot downloads should succeed
3. **User Adoption**: >80% of new nodes should use snapshots
4. **Performance**: Snapshot downloads should complete in <10 minutes
5. **Reliability**: <1% of snapshots should have validation failures
