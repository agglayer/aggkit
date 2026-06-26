package rpcclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
)

var jSONRPCCallWithContext = rpc.JSONRPCCallWithContext

const defaultRequestTimeout = 10 * time.Second

// Client wraps all the available endpoints of the data abailability committee node server
type Client struct {
	url            string
	requestTimeout time.Duration
}

func NewClient(url string) *Client {
	return &Client{
		url:            url,
		requestTimeout: defaultRequestTimeout,
	}
}

func (c *Client) GetStatus() (*types.AggsenderInfo, error) {
	response, err := c.call("aggsender_status")
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
	response, err := c.call("aggsender_getCertificateHeaderPerHeight", height)
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
	response, err := c.call("aggsender_getCertificateBridgeExits", height)
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

func (c *Client) call(method string, params ...interface{}) (rpc.Response, error) {
	timeout := c.requestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return jSONRPCCallWithContext(ctx, c.url, method, params...)
}
