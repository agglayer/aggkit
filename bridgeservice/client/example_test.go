package client_test

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
)

func ExampleNew() {
	// Create a client with default timeout (30 seconds)
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	fmt.Printf("Client created with base URL: %s\n", "http://localhost:8080")

	// Create a client with custom timeout
	c = client.New(client.Config{
		BaseURL: "http://localhost:8080",
		Timeout: 10 * time.Second,
	})

	_ = c
}

func ExampleClient_HealthCheck() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	resp, err := c.HealthCheck(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Status: %s\n", resp.Status)
}

func ExampleClient_GetBridges() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	// Minimal parameters
	resp, err := c.GetBridges(context.Background(), client.GetBridgesParams{
		NetworkID: 1,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Total bridges: %d\n", resp.Count)

	// With optional parameters
	pageNum := uint32(1)
	pageSize := uint32(20)
	depositCount := uint64(10)
	fromAddr := "0x1234567890123456789012345678901234567890"

	resp, err = c.GetBridges(context.Background(), client.GetBridgesParams{
		NetworkID:    1,
		PageNumber:   &pageNum,
		PageSize:     &pageSize,
		DepositCount: &depositCount,
		FromAddress:  &fromAddr,
		NetworkIDs:   []uint32{2, 3},
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, bridge := range resp.Bridges {
		fmt.Printf("Bridge at block %d\n", bridge.BlockNum)
	}
}

func ExampleClient_GetClaims() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	includeAll := true
	globalIndex := big.NewInt(123)

	resp, err := c.GetClaims(context.Background(), client.GetClaimsParams{
		NetworkID:        1,
		IncludeAllFields: &includeAll,
		GlobalIndex:      globalIndex,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Total claims: %d\n", resp.Count)
}

func ExampleClient_GetUnsetClaims() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	pageNum := 1
	pageSize := 10
	globalIndex := big.NewInt(456)

	resp, err := c.GetUnsetClaims(context.Background(), client.GetUnsetClaimsParams{
		PageNumber:  &pageNum,
		PageSize:    &pageSize,
		GlobalIndex: globalIndex,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Total unset claims: %d\n", resp.Count)
}

func ExampleClient_GetSetClaims() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	pageNum := 1
	pageSize := 10

	resp, err := c.GetSetClaims(context.Background(), client.GetSetClaimsParams{
		PageNumber: &pageNum,
		PageSize:   &pageSize,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Total set claims: %d\n", resp.Count)
}

func ExampleClient_GetTokenMappings() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	tokenAddr := "0xabcdef0123456789abcdef0123456789abcdef01"

	resp, err := c.GetTokenMappings(context.Background(), client.GetTokenMappingsParams{
		NetworkID:          1,
		OriginTokenAddress: &tokenAddr,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Total token mappings: %d\n", resp.Count)
}

func ExampleClient_GetLegacyTokenMigrations() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	resp, err := c.GetLegacyTokenMigrations(context.Background(), client.GetLegacyTokenMigrationsParams{
		NetworkID: 2,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Total legacy token migrations: %d\n", resp.Count)
}

func ExampleClient_GetL1InfoTreeIndex() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	index, err := c.GetL1InfoTreeIndex(context.Background(), 1, 10)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("L1 Info Tree Index: %d\n", index)
}

func ExampleClient_GetInjectedL1InfoLeaf() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	resp, err := c.GetInjectedL1InfoLeaf(context.Background(), 2, 5)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("L1 Info Tree Index: %d, Block: %d\n", resp.L1InfoTreeIndex, resp.BlockNumber)
}

func ExampleClient_GetClaimProof() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	proof, err := c.GetClaimProof(context.Background(), 1, 10, 5)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Claim proof retrieved for L1 Info Tree Index: %d\n", proof.L1InfoTreeLeaf.L1InfoTreeIndex)
}

func ExampleClient_GetLastReorgEvent() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	// For L1 (networkID = 0)
	resp, err := c.GetLastReorgEvent(context.Background(), 0)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Last reorg from block %d to block %d\n", resp.FromBlock, resp.ToBlock)
}

func ExampleClient_GetSyncStatus() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	resp, err := c.GetSyncStatus(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("L1 Synced: %v, L2 Synced: %v\n", resp.L1Info.IsSynced, resp.L2Info.IsSynced)
}

func ExampleClient_GetRemoveGEREvents() {
	c := client.New(client.Config{
		BaseURL: "http://localhost:8080",
	})

	ger := "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	limit := 25

	resp, err := c.GetRemoveGEREvents(context.Background(), client.GetRemoveGEREventsParams{
		GlobalExitRoot: &ger,
		Limit:          &limit,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Total remove GER events: %d\n", resp.Count)
}
