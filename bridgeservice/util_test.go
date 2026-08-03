package bridgeservice

import (
	"math/big"
	"net/http"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNetworkIDSliceParam(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		queryParams    string
		expectedResult []uint32
		expectedError  string
	}{
		{
			name:           "valid network IDs within limit",
			queryParams:    "network_ids=1&network_ids=2&network_ids=3",
			expectedResult: []uint32{1, 2, 3},
		},
		{
			name:           "exactly 5 network IDs (at limit)",
			queryParams:    "network_ids=1&network_ids=2&network_ids=3&network_ids=4&network_ids=5",
			expectedResult: []uint32{1, 2, 3, 4, 5},
		},
		{
			name:          "too many network IDs (exceeds limit)",
			queryParams:   "network_ids=1&network_ids=2&network_ids=3&network_ids=4&network_ids=5&network_ids=6",
			expectedError: "too many network IDs provided: maximum 5 allowed, got 6",
		},
		{
			name:          "invalid network ID",
			queryParams:   "network_ids=1&network_ids=abc",
			expectedError: "invalid network ID 'abc':",
		},
		{
			name:           "empty parameter",
			queryParams:    "",
			expectedResult: []uint32{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(nil)
			c.Request = &http.Request{
				URL: &url.URL{RawQuery: tt.queryParams},
			}

			result, err := parseNetworkIDSliceParam(c, networkIDsParam)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}
		})
	}
}

func TestParseBigIntQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		queryParams    string
		expectedResult *big.Int
		expectedError  string
	}{
		{
			name:           "valid number",
			queryParams:    "global_index=1000000",
			expectedResult: big.NewInt(1000000),
		},
		{
			name:           "empty parameter",
			queryParams:    "",
			expectedResult: nil,
		},
		{
			name:          "invalid input",
			queryParams:   "global_index=invalid",
			expectedError: "invalid global_index parameter, it should be a numeric",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(nil)
			c.Request = &http.Request{
				URL: &url.URL{RawQuery: tt.queryParams},
			}

			result, err := parseBigIntQuery(c)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				if tt.expectedResult == nil {
					assert.Nil(t, result)
				} else {
					require.NotNil(t, result)
					assert.Equal(t, 0, tt.expectedResult.Cmp(result),
						"expected %s, got %s", tt.expectedResult.String(), result.String())
				}
			}
		})
	}
}
