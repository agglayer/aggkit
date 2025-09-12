package query

import (
	"errors"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/aggchain-ecdsa-multisig/aggchainbase"
	"github.com/agglayer/aggkit/aggsender/mocks"
	typesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_ECDSAMultisigCommitteeQuery_GetMultisigCommittee(t *testing.T) {
	type testCase struct {
		name               string
		threshold          *big.Int
		signerInfos        []aggchainbase.IAggchainSignersSignerInfo
		overrideURL        *CommitteeOverride
		thresholdErr       error
		getSignersErr      error
		expectedErr        string
		expectedNumSigners int
		expectedSigner     string
	}

	testCases := []testCase{
		{
			name:      "successfully returns committee",
			threshold: big.NewInt(2),
			signerInfos: []aggchainbase.IAggchainSignersSignerInfo{
				{
					Addr: common.HexToAddress("0x1"),
					Url:  "http://localhost:8001",
				},
				{
					Addr: common.HexToAddress("0x2"),
					Url:  "http://localhost:8002",
				},
			},
			expectedErr:        "",
			expectedNumSigners: 2,
		},
		{
			name:         "threshold query fails",
			thresholdErr: errors.New("threshold error"),
			expectedErr:  "failed to query the signatures threshold",
		},
		{
			name:          "signers query fails",
			threshold:     big.NewInt(1),
			getSignersErr: errors.New("signers error"),
			expectedErr:   "failed to query the committee signers",
		},
		{
			name:      "successfully returns committee",
			threshold: big.NewInt(2),
			signerInfos: []aggchainbase.IAggchainSignersSignerInfo{
				{
					Addr: common.HexToAddress("0x1"),
					Url:  "http://localhost:8001",
				},
				{
					Addr: common.HexToAddress("0x2"),
					Url:  "http://localhost:8002",
				},
			},
			overrideURL: &CommitteeOverride{
				URLMapping: map[string]string{
					"http://localhost:8001": "http://override1:8001",
					"http://localhost:8002": "http://override2:8002",
				},
			},
			expectedErr:        "",
			expectedNumSigners: 2,
			expectedSigner:     "{Committee: {0x0000000000000000000000000000000000000001=http://override1:8001, 0x0000000000000000000000000000000000000002=http://override2:8002},  Threshold: 2}",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockSC := new(mocks.MultisigContract)

			mockSC.EXPECT().Threshold(mock.Anything).
				Return(tc.threshold, tc.thresholdErr)

			if tc.thresholdErr == nil {
				mockSC.EXPECT().GetAggchainSignerInfos(mock.Anything).
					Return(tc.signerInfos, tc.getSignersErr)
			}

			q := &BaseMultisigCommitteeQuery{
				sovereignRollupAddrSC: mockSC,
				sovereignRollupAddr:   common.Address{},
				overrideURL:           tc.overrideURL,
			}

			blockNum := big.NewInt(100)
			committee, err := q.GetMultisigCommittee(t.Context(), blockNum)

			if tc.expectedErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedErr)
				require.Nil(t, committee)
			} else {
				require.NoError(t, err)
				require.NotNil(t, committee)
				require.Equal(t, tc.threshold, committee.Threshold())
				require.Len(t, committee.Signers(), tc.expectedNumSigners)
				if tc.expectedSigner != "" {
					require.Equal(t, tc.expectedSigner, committee.String())
				}
			}

			mockSC.AssertExpectations(t)
		})
	}
}

func Test_CommitteeURLOverride(t *testing.T) {
	t.Run("ReplaceURL replaces URLs based on the override map", func(t *testing.T) {
		override := &CommitteeOverride{
			URLMapping: map[string]string{
				"http://original1": "http://override1",
				"http://original3": "http://override3",
			},
		}

		committee := []aggchainbase.IAggchainSignersSignerInfo{
			{
				Addr: common.HexToAddress("0x1"),
				Url:  "http://original1",
			},
			{
				Addr: common.HexToAddress("0x2"),
				Url:  "http://original2",
			},
			{
				Addr: common.HexToAddress("0x3"),
				Url:  "http://original3",
			},
		}

		expected := []aggchainbase.IAggchainSignersSignerInfo{
			{
				Addr: common.HexToAddress("0x1"),
				Url:  "http://override1",
			},
			{
				Addr: common.HexToAddress("0x2"),
				Url:  "http://original2",
			},
			{
				Addr: common.HexToAddress("0x3"),
				Url:  "http://override3",
			},
		}

		result := override.ReplaceURL(committee)
		require.Equal(t, expected, result)
	})
	t.Run("ReplaceURL returns nil if override is nil", func(t *testing.T) {
		var override *CommitteeOverride = nil

		committee := []aggchainbase.IAggchainSignersSignerInfo{
			{
				Addr: common.HexToAddress("0x1"),
				Url:  "http://original1",
			},
		}

		result := override.ReplaceURL(committee)
		require.Equal(t, committee, result)
	})
	t.Run("ReplaceURL returns nil if override map is empty", func(t *testing.T) {
		override := &CommitteeOverride{
			URLMapping: map[string]string{},
		}

		committee := []aggchainbase.IAggchainSignersSignerInfo{
			{
				Addr: common.HexToAddress("0x1"),
				Url:  "http://original1",
			},
		}

		result := override.ReplaceURL(committee)
		require.Equal(t, committee, result)
	})
}

func Test_CommitteeURLOverride_String(t *testing.T) {
	require.Equal(t, "CommitteeOverride{URL: map[oldURL1:newURL1 oldURL2:newURL2]}", (&CommitteeOverride{
		URLMapping: map[string]string{
			"oldURL1": "newURL1",
			"oldURL2": "newURL2",
		},
	}).String())
	require.Equal(t, "CommitteeOverride{URL: map[]}", (&CommitteeOverride{}).String())
	var CommitteeOverride *CommitteeOverride = nil
	require.Equal(t, "CommitteeOverride{nil}", CommitteeOverride.String())
}

func Test_NewBaseMultisigCommitteeQuery(t *testing.T) {
	t.Run("successfully creates a new BaseMultisigCommitteeQuery", func(t *testing.T) {
		mockClient := typesmocks.NewEthClienter(t)
		rollupAddr := common.HexToAddress("0x123")

		//mockClient.EXPECT().CodeAt(mock.Anything, rollupAddr, mock.Anything).Return([]byte{0x1, 0x2}, nil)

		query, err := NewBaseMultisigCommitteeQuery(rollupAddr, mockClient, nil)
		require.NoError(t, err)
		require.NotNil(t, query)
		require.Equal(t, rollupAddr, query.sovereignRollupAddr)
		require.NotNil(t, query.sovereignRollupAddrSC)
	})

}
