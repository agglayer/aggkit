package query

import (
	"errors"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	typesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_ECDSAMultisigCommitteeQuery_GetMultisigCommittee(t *testing.T) {
	type testCase struct {
		name               string
		threshold          uint64
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
			threshold: 2,
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
			threshold:     1,
			getSignersErr: errors.New("signers error"),
			expectedErr:   "failed to query the committee signers",
		},
		{
			name:      "successfully returns committee",
			threshold: 2,
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
			th := big.NewInt(int64(tc.threshold))
			mockSC.EXPECT().Threshold(mock.Anything).
				Return(th, tc.thresholdErr)

			if tc.thresholdErr == nil {
				mockSC.EXPECT().GetAggchainSignerInfos(mock.Anything).
					Return(tc.signerInfos, tc.getSignersErr)
			}

			q := &BaseMultisigCommitteeQuery{
				sovereignRollupSC:   mockSC,
				sovereignRollupAddr: common.Address{},
				overrideURL:         tc.overrideURL,
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
	type testCase struct {
		name      string
		override  *CommitteeOverride
		committee []aggchainbase.IAggchainSignersSignerInfo
		expected  []aggchainbase.IAggchainSignersSignerInfo
	}

	testCases := []testCase{
		{
			name: "ReplaceURL replaces URLs based on the override map",
			override: &CommitteeOverride{
				URLMapping: map[string]string{
					"http://original1": "http://override1",
					"http://original3": "http://override3",
				},
			},
			committee: []aggchainbase.IAggchainSignersSignerInfo{
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
			},
			expected: []aggchainbase.IAggchainSignersSignerInfo{
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
			},
		},
		{
			name:     "ReplaceURL returns input if override is nil",
			override: nil,
			committee: []aggchainbase.IAggchainSignersSignerInfo{
				{
					Addr: common.HexToAddress("0x1"),
					Url:  "http://original1",
				},
			},
			expected: []aggchainbase.IAggchainSignersSignerInfo{
				{
					Addr: common.HexToAddress("0x1"),
					Url:  "http://original1",
				},
			},
		},
		{
			name: "ReplaceURL returns input if override map is empty",
			override: &CommitteeOverride{
				URLMapping: map[string]string{},
			},
			committee: []aggchainbase.IAggchainSignersSignerInfo{
				{
					Addr: common.HexToAddress("0x1"),
					Url:  "http://original1",
				},
			},
			expected: []aggchainbase.IAggchainSignersSignerInfo{
				{
					Addr: common.HexToAddress("0x1"),
					Url:  "http://original1",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.override.ReplaceURL(tc.committee)
			require.Equal(t, tc.expected, result)
		})
	}
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
		query, err := NewBaseMultisigCommitteeQuery(rollupAddr, mockClient, nil)
		require.NoError(t, err)
		require.NotNil(t, query)
		require.Equal(t, rollupAddr, query.sovereignRollupAddr)
		require.NotNil(t, query.sovereignRollupSC)
	})
}

func Test_ContractMode(t *testing.T) {
	type testCase struct {
		name                string
		consensusTypeReturn uint32
		consensusTypeErr    error
		aggchainTypeReturn  [2]byte
		aggchainTypeErr     error
		expectedMode        types.AggsenderMode
		expectedErr         string // if "" no error
	}
	errGeneric := errors.New("some error")
	testCases := []testCase{
		{
			name:                "error CONSENSUSTYPE",
			consensusTypeReturn: 0,
			expectedErr:         "consensus type must be 1 always",
		},
		{
			name:             "error getting CONSENSUSTYPE",
			consensusTypeErr: errGeneric,
			expectedErr:      "failed to get consensus type",
		},
		{
			name:                "error getting AGGCHAINTYPE",
			consensusTypeReturn: consensusTypeMultiECDSAAndSP1,
			aggchainTypeErr:     errGeneric,
			expectedErr:         "failed to get aggchain type",
		},
		{
			name:                "return PessimisticProofMode",
			consensusTypeReturn: consensusTypeMultiECDSAAndSP1,
			aggchainTypeReturn:  aggchainECDSAMultisig,
			expectedMode:        types.PessimisticProofMode,
		},
		{
			name:                "return AggchainProofMode",
			consensusTypeReturn: consensusTypeMultiECDSAAndSP1,
			aggchainTypeReturn:  aggchainFEP,
			expectedMode:        types.AggchainProofMode,
		},
		{
			name:                "unknown AGGCHAINTYPE",
			consensusTypeReturn: consensusTypeMultiECDSAAndSP1,
			aggchainTypeReturn:  [2]byte{0xFF, 0xFF},
			expectedErr:         "unsupported aggchain type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockSC := new(mocks.MultisigContract)
			sut := &BaseMultisigCommitteeQuery{
				sovereignRollupSC:   mockSC,
				sovereignRollupAddr: common.Address{},
			}
			mockSC.EXPECT().CONSENSUSTYPE(mock.Anything).
				Return(tc.consensusTypeReturn, tc.consensusTypeErr).Maybe()
			mockSC.EXPECT().AGGCHAINTYPE(mock.Anything).
				Return(tc.aggchainTypeReturn, tc.aggchainTypeErr).Maybe()

			mode, err := sut.ContractMode()
			if tc.expectedErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedMode, mode)
			}
			mockSC.AssertExpectations(t)
		})
	}
}

func Test_ResolveAutoMode(t *testing.T) {
	type testCase struct {
		name               string
		aggchainTypeReturn [2]byte
		aggchainTypeErr    error
		cfgMode            types.AggsenderMode
		expectedMode       types.AggsenderMode
		expectedErr        string // if "" no error
	}
	errGeneric := errors.New("some error")
	testCases := []testCase{

		{
			name:            "error getting ContractMode",
			aggchainTypeErr: errGeneric,
			cfgMode:         types.AutoMode,
			expectedErr:     "aggsender mode is AUTO, but can't get contract mode",
		},
		{
			name:            "dont ask for ContractMode due cfg is not AUTO",
			aggchainTypeErr: errGeneric,
			cfgMode:         types.AggchainProofMode,
			expectedMode:    types.AggchainProofMode,
		},
		{
			name:               "return PessimisticProofMode",
			aggchainTypeReturn: aggchainECDSAMultisig,
			cfgMode:            types.AutoMode,
			expectedMode:       types.PessimisticProofMode,
		},
		{
			name:               "return AggchainProofMode",
			aggchainTypeReturn: aggchainFEP,
			cfgMode:            types.AutoMode,
			expectedMode:       types.AggchainProofMode,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockSC := new(mocks.MultisigContract)
			sut := &BaseMultisigCommitteeQuery{
				sovereignRollupSC:   mockSC,
				sovereignRollupAddr: common.Address{},
			}
			mockSC.EXPECT().CONSENSUSTYPE(mock.Anything).
				Return(consensusTypeMultiECDSAAndSP1, nil).Maybe()
			mockSC.EXPECT().AGGCHAINTYPE(mock.Anything).
				Return(tc.aggchainTypeReturn, tc.aggchainTypeErr).Maybe()

			mode, err := sut.ResolveAutoMode(tc.cfgMode)
			if tc.expectedErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedMode, mode)
			}
			mockSC.AssertExpectations(t)
		})
	}
}
