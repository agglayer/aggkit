package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/apiclient"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
)

const (
	errLevelUnexpected         = 1
	errLevelWrongParams        = 2
	errLevelComms              = 3
	errLevelNotFound           = 4
	errLevelFoundButNotSettled = 5

	base10         = 10
	minimumNumArgs = 3
)

func unmarshalGlobalIndex(globalIndex string) (*agglayertypes.GlobalIndex, error) {
	var globalIndexParsed agglayertypes.GlobalIndex
	// First try if it's already decomposed
	err := json.Unmarshal([]byte(globalIndex), &globalIndexParsed)
	if err != nil {
		bigInt := new(big.Int)
		_, ok := bigInt.SetString(globalIndex, base10)
		if !ok {
			return nil, fmt.Errorf("invalid global index: %v", globalIndex)
		}
		mainnetFlag, rollupIndex, leafIndex, err := bridgesync.DecodeGlobalIndex(bigInt)
		if err != nil {
			return nil, fmt.Errorf("invalid global index, fail to decode: %v", globalIndex)
		}
		globalIndexParsed.MainnetFlag = mainnetFlag
		globalIndexParsed.RollupIndex = rollupIndex
		globalIndexParsed.LeafIndex = leafIndex
	}
	return &globalIndexParsed, nil
}

// certificateContainsGlobalIndex reports whether the certificate at height
// imports the given global index.
func certificateContainsGlobalIndex(aggsenderClient *apiclient.Client, height uint64,
	globalIndex *agglayertypes.GlobalIndex) (bool, error) {
	importedBridgeExits, err := aggsenderClient.GetCertificateImportedBridgeExits(&height)
	if err != nil {
		if errors.Is(err, apiclient.ErrNotFound) {
			return false, nil
		}
		return false, err
	}

	for _, importedBridge := range importedBridgeExits {
		if importedBridge.GlobalIndex != nil && *importedBridge.GlobalIndex == *globalIndex {
			return true, nil
		}
	}

	return false, nil
}

func main() {
	if len(os.Args) != minimumNumArgs {
		fmt.Printf("Wrong number of arguments\n")
		fmt.Printf(" Usage: %v <aggkitAPIURL> <globalIndex>\n", os.Args[0])
		os.Exit(errLevelWrongParams)
	}
	aggkitAPIURL := os.Args[1]
	globalIndex := os.Args[2]
	decodedGlobalIndex, err := unmarshalGlobalIndex(globalIndex)
	if err != nil {
		log.Errorf("Error unmarshalGlobalIndex: %v", err)
		os.Exit(errLevelWrongParams)
	}
	log.Debugf("decodedGlobalIndex: %v", decodedGlobalIndex)
	aggsenderClient := apiclient.NewClient(aggkitAPIURL)
	// The last sent certificate header tells which height to start from.
	lastHeader, err := aggsenderClient.GetCertificateHeaderPerHeight(nil)
	if err != nil {
		log.Errorf("Error: %v", err)
		os.Exit(errLevelComms)
	}

	currentHeight := lastHeader.Height
	for {
		found, err := certificateContainsGlobalIndex(aggsenderClient, currentHeight, decodedGlobalIndex)
		if err != nil {
			log.Errorf("Error: %v", err)
			os.Exit(errLevelUnexpected)
		}
		if found {
			log.Infof("Found certificate for global index: %v", globalIndex)
			header, err := aggsenderClient.GetCertificateHeaderPerHeight(&currentHeight)
			if err != nil {
				log.Errorf("Error: %v", err)
				os.Exit(errLevelComms)
			}
			if header.Status.IsSettled() {
				log.Infof("Certificate is settled: %s status:%s", header.ID(), header.Status.String())
				os.Exit(0)
			}
			log.Errorf("Certificate is not settled")
			os.Exit(errLevelFoundButNotSettled)
		}

		log.Debugf("Certificate not found for global index: %v", globalIndex)
		// We have checked the oldest cert
		if currentHeight == 0 {
			log.Errorf("Checked all certs and it's not found")
			os.Exit(errLevelNotFound)
		}
		currentHeight--
		log.Infof("Checking previous certificate, height: %v", currentHeight)
	}
}
