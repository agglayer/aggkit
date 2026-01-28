# Bridge Service Client

A Go client library for interacting with the Bridge Service REST API.

## Installation

```bash
go get github.com/agglayer/aggkit/bridgeservice/client
```

## Usage

### Creating a Client

```go
import "github.com/agglayer/aggkit/bridgeservice/client"

// Create a client with default timeout (30 seconds)
c := client.New(client.Config{
    BaseURL: "http://localhost:8080",
})

// Create a client with custom timeout
c := client.New(client.Config{
    BaseURL: "http://localhost:8080",
    Timeout: 10 * time.Second,
})
```

### Health Check

```go
resp, err := c.HealthCheck(ctx)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Status: %s, Version: %s\n", resp.Status, resp.Version)
```

### Get Bridges

```go
// Minimal parameters
resp, err := c.GetBridges(ctx, client.GetBridgesParams{
    NetworkID: 1,
})

// With optional parameters
pageNum := uint32(1)
pageSize := uint32(20)
depositCount := uint64(10)
fromAddr := "0x1234567890123456789012345678901234567890"

resp, err := c.GetBridges(ctx, client.GetBridgesParams{
    NetworkID:    1,
    PageNumber:   &pageNum,
    PageSize:     &pageSize,
    DepositCount: &depositCount,
    FromAddress:  &fromAddr,
    NetworkIDs:   []uint32{2, 3},
})
```

### Get Claims

```go
// Minimal parameters
resp, err := c.GetClaims(ctx, client.GetClaimsParams{
    NetworkID: 1,
})

// With optional parameters
includeAll := true
globalIndex := big.NewInt(123)

resp, err := c.GetClaims(ctx, client.GetClaimsParams{
    NetworkID:        1,
    IncludeAllFields: &includeAll,
    GlobalIndex:      globalIndex,
})
```

### Get Unset Claims (L2 only)

```go
pageNum := 1
pageSize := 10
globalIndex := big.NewInt(456)

resp, err := c.GetUnsetClaims(ctx, client.GetUnsetClaimsParams{
    PageNumber:  &pageNum,
    PageSize:    &pageSize,
    GlobalIndex: globalIndex,
})
```

### Get Set Claims (L2 only)

```go
resp, err := c.GetSetClaims(ctx, client.GetSetClaimsParams{
    PageNumber: &pageNum,
    PageSize:   &pageSize,
})
```

### Get Token Mappings

```go
tokenAddr := "0xabcdef0123456789abcdef0123456789abcdef01"

resp, err := c.GetTokenMappings(ctx, client.GetTokenMappingsParams{
    NetworkID:          1,
    OriginTokenAddress: &tokenAddr,
})
```

### Get Legacy Token Migrations

```go
resp, err := c.GetLegacyTokenMigrations(ctx, client.GetLegacyTokenMigrationsParams{
    NetworkID: 2,
})
```

### Get L1 Info Tree Index

```go
index, err := c.GetL1InfoTreeIndex(ctx, 1, 10)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("L1 Info Tree Index: %d\n", index)
```

### Get Injected L1 Info Leaf

```go
resp, err := c.GetInjectedL1InfoLeaf(ctx, 2, 5)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("L1 Info Tree Index: %d\n", resp.L1InfoTreeIndex)
```

### Get Claim Proof

```go
proof, err := c.GetClaimProof(ctx, 1, 10, 5)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Proof Local Exit Root: %v\n", proof.ProofLocalExitRoot)
```

### Get Last Reorg Event

```go
// For L1 (networkID = 0)
resp, err := c.GetLastReorgEvent(ctx, 0)

// For L2
resp, err := c.GetLastReorgEvent(ctx, 1)
```

### Get Sync Status

```go
resp, err := c.GetSyncStatus(ctx)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("L1 Synced: %v, L2 Synced: %v\n", resp.L1Info.IsSynced, resp.L2Info.IsSynced)
```

### Get Remove GER Events

```go
// Without filters
resp, err := c.GetRemoveGEREvents(ctx, client.GetRemoveGEREventsParams{})

// With filters
ger := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
limit := 25

resp, err := c.GetRemoveGEREvents(ctx, client.GetRemoveGEREventsParams{
    GlobalExitRoot: &ger,
    Limit:          &limit,
})
```

## Error Handling

All methods return errors that should be checked:

```go
resp, err := c.GetBridges(ctx, params)
if err != nil {
    // Handle error
    // Error messages include HTTP status codes and response bodies
    log.Printf("Error: %v", err)
    return
}
// Use response
```

## Context Support

All API methods accept a `context.Context` parameter for cancellation and timeout control:

```go
// With timeout
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

resp, err := c.GetBridges(ctx, params)

// With cancellation
ctx, cancel := context.WithCancel(context.Background())
// Cancel when needed
cancel()
```

## Testing

Run the tests:

```bash
go test ./bridgeservice/client/...
```

Run with coverage:

```bash
go test -cover ./bridgeservice/client/...
```

Current test coverage: **91.9%**
