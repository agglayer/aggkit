package rpcclient

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"

	"github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggsenderrpc "github.com/agglayer/aggkit/aggsender/rpc"
	"github.com/agglayer/aggkit/aggsender/types"
	ethCommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var jSONRPCCall = rpc.JSONRPCCall

// Client wraps all the available endpoints of the data abailability committee node server
type Client struct {
	url string
}

func NewClient(url string) *Client {
	return &Client{
		url: url,
	}
}

func (c *Client) GetStatus() (*types.AggsenderInfo, error) {
	response, err := jSONRPCCall(c.url, "aggsender_status")
	if err != nil {
		return nil, err
	}

	// Check if the response is an error
	if response.Error != nil {
		return nil, fmt.Errorf("error in the response calling aggsender_status: %v", response.Error)
	}
	result := types.AggsenderInfo{}
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetCertificateHeaderPerHeight(height *uint64) (*types.Certificate, error) {
	response, err := jSONRPCCall(c.url, "aggsender_getCertificateHeaderPerHeight", height)
	if err != nil {
		return nil, err
	}

	// Check if the response is an error
	if response.Error != nil {
		return nil, fmt.Errorf("error in the response calling aggsender_getCertificateHeaderPerHeight: %v", response.Error)
	}
	cert := types.Certificate{}
	err = json.Unmarshal(response.Result, &cert)
	if err != nil {
		return nil, err
	}
	return &cert, nil
}

// GetCertificateBridgeExits returns the bridge exits for the certificate at the given height.
// If height is nil, returns the bridge exits of the last sent certificate.
func (c *Client) GetCertificateBridgeExits(height *uint64) ([]*agglayertypes.BridgeExit, error) {
	response, err := jSONRPCCall(c.url, "aggsender_getCertificateBridgeExits", height)
	if err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, fmt.Errorf("error in the response calling aggsender_getCertificateBridgeExits: %v", response.Error)
	}
	var exits []*agglayertypes.BridgeExit
	if err := json.Unmarshal(response.Result, &exits); err != nil {
		return nil, err
	}
	return exits, nil
}

// DebugSendCertificate signs the certificate with the given private key and sends it via the debug endpoint.
// The hashing and signing are handled internally; callers just pass the cert and key.
func (c *Client) DebugSendCertificate(cert *agglayertypes.Certificate, privateKey *ecdsa.PrivateKey) (ethCommon.Hash, error) {
	hash, err := aggsenderrpc.HashCertificateForDebugAuth(cert)
	if err != nil {
		return ethCommon.Hash{}, fmt.Errorf("DebugSendCertificate: hash error: %w", err)
	}
	sig, err := crypto.Sign(hash.Bytes(), privateKey)
	if err != nil {
		return ethCommon.Hash{}, fmt.Errorf("DebugSendCertificate: sign error: %w", err)
	}
	req := aggsenderrpc.DebugSendCertificateRequest{
		Certificate: *cert,
		Signature:   sig,
	}
	response, err := jSONRPCCall(c.url, "aggsender_debugSendCertificate", req)
	if err != nil {
		return ethCommon.Hash{}, err
	}
	if response.Error != nil {
		return ethCommon.Hash{}, fmt.Errorf("error in response for aggsender_debugSendCertificate: %v", response.Error)
	}
	var certHash ethCommon.Hash
	if err := json.Unmarshal(response.Result, &certHash); err != nil {
		return ethCommon.Hash{}, err
	}
	return certHash, nil
}
