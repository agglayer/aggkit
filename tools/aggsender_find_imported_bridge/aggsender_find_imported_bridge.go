package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/rpcclient"
	"github.com/agglayer/aggkit/aggsender/types"
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
		firstUnusedBits, mainnetFlag, rollupIndex, leafIndex := bridgesync.DecodeGlobalIndex(bigInt)
		globalIndexParsed.MainnetFlag = mainnetFlag
		globalIndexParsed.RollupIndex = rollupIndex
		globalIndexParsed.LeafIndex = leafIndex
		globalIndexParsed.FirstUnusedBits = firstUnusedBits
	}
	return &globalIndexParsed, nil
}

// This function find out the certificate for a deposit
// It use the aggsender RPC
func certContainsGlobalIndex(cert *types.Certificate, globalIndex *agglayertypes.GlobalIndex) (bool, error) {
	if cert == nil {
		return false, nil
	}
	certSigned, err := tryUnmarshalCertificate(cert)
	if err != nil {
		log.Debugf("cert: %v", cert.SignedCertificate)
		return false, fmt.Errorf("error Unmarshal cert. Err: %w", err)
	}
	for _, importedBridge := range certSigned.ImportedBridgeExits {
		if *importedBridge.GlobalIndex == *globalIndex {
			return true, nil
		}
	}
	return false, nil
}

func tryUnmarshalCertificate(cert *types.Certificate) (*agglayertypes.Certificate, error) {
	var certSigned agglayertypes.Certificate
	err := json.Unmarshal([]byte(*cert.SignedCertificate), &certSigned)
	if err == nil {
		return &certSigned, nil
	}

	log.Warnf("Error unmarshal new certificate format: %v. It will fallback to the old one", err)

	var certSignedOld agglayertypes.SignedCertificate
	err = json.Unmarshal([]byte(*cert.SignedCertificate), &certSignedOld)
	if err != nil {
		log.Errorf("Could not unmarshal certificate with old format: %v", err)
		return nil, fmt.Errorf("error Unmarshal cert. Err: %w", err)
	}

	return certSignedOld.Certificate, nil
}

func main() {
	if len(os.Args) != minimumNumArgs {
		fmt.Printf("Wrong number of arguments\n")
		fmt.Printf(" Usage: %v <aggsenderRPC> <globalIndex>\n", os.Args[0])
		os.Exit(errLevelWrongParams)
	}
	aggsenderRPC := os.Args[1]
	globalIndex := os.Args[2]
	decodedGlobalIndex, err := unmarshalGlobalIndex(globalIndex)
	if err != nil {
		log.Errorf("Error unmarshalGlobalIndex: %v", err)
		os.Exit(errLevelWrongParams)
	}
	log.Debugf("decodedGlobalIndex: %v", decodedGlobalIndex)
	aggsenderClient := rpcclient.NewClient(aggsenderRPC)
	// Get latest certificate
	cert, err := aggsenderClient.GetCertificateHeaderPerHeight(nil)
	if err != nil {
		log.Errorf("Error: %v", err)
		os.Exit(errLevelComms)
	}

	currentHeight := cert.Header.Height
	for cert != nil {
		found, err := certContainsGlobalIndex(cert, decodedGlobalIndex)
		if err != nil {
			log.Errorf("Error: %v", err)
			os.Exit(errLevelUnexpected)
		}
		if found {
			log.Infof("Found certificate for global index: %v", globalIndex)
			if cert.Header.Status.IsSettled() {
				log.Infof("Certificate is settled: %s status:%s", cert.Header.ID(), cert.Header.Status.String())
				os.Exit(0)
			}
			log.Errorf("Certificate is not settled")
			os.Exit(errLevelFoundButNotSettled)
		} else {
			log.Debugf("Certificate not found for global index: %v", globalIndex)
		}
		// We have check the oldest cert
		if currentHeight == 0 {
			log.Errorf("Checked all certs and it's not found")
			os.Exit(errLevelNotFound)
		}
		currentHeight--
		log.Infof("Checking previous certificate, height: %v", currentHeight)
		cert, err = aggsenderClient.GetCertificateHeaderPerHeight(&currentHeight)
		if err != nil {
			log.Errorf("Error: %v", err)
			os.Exit(errLevelComms)
		}
	}
}
