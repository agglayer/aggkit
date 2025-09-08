package query

import (
	"errors"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/aggchain-ecdsa-multisig/aggchainecdsamultisig"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_ECDSAMultisigCommitteeQuery_GetMultisigCommittee(t *testing.T) {
	type testCase struct {
		name               string
		threshold          *big.Int
		signerInfos        []aggchainecdsamultisig.IAggchainSignersSignerInfo
		thresholdErr       error
		getSignersErr      error
		expectedErr        string
		expectedNumSigners int
	}

	testCases := []testCase{
		{
			name:      "successfully returns committee",
			threshold: big.NewInt(2),
			signerInfos: []aggchainecdsamultisig.IAggchainSignersSignerInfo{
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

			q := &ECDSAMultisigCommitteeQuery{
				multisigCommitteeSC:   mockSC,
				multisigCommitteeAddr: common.Address{},
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
			}

			mockSC.AssertExpectations(t)
		})
	}
}
