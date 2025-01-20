package aggsender

import (
	"testing"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// LOCAL			| AGGLAYER SETTLED		| AGGLAYER PENDING			| ACTION
//--------------------------------------------------------------------------------------------------
// ID | h  | st     | ID | h  | st			| ID | h  | st   			|
// -------------------------------------------------------------------------------------------------

// nil 				| nil 					| nil 		   					| none
// nil 				| nil 					| ID1| NA   | inError  			| none
// nil 				| nil 					| ID1| h0   | !=inError  		| store(PENDING)
// nil 				| ID1| h1 | NA	 		| nil 							| store(SETTLE)
// nil 				| ID1| h1 | NA	 		| ID2| h2   | inError  			| store(SETTLE)
// nil 				| ID1| h1 | NA	 		| ID2| h2   | !=inError  		| store(SETTLE)

// ID1| h1 | NA		| nil 					| ID1| h1   | inError  			| update(PENDING)
// ID1| h1 | NA		| nil 					| ID1| h1   | !=inError  		| update(PENDING)
// ID2| h2 | NA		| ID1| h1 | N/A			| ID2| h2 	| N/A		  		| update(PENDING)
// ID2| h2 | NA		| ID1| h3 | N/A			| nil               			| update(h2, ask_agglayer(h2)) + store(SETTLED)
// ID2| h2 | NA		| ID1| h2 | settled		| ID1| h3 | !=inError           | update(SETTLED) + store(PENDING)
// ID2| h2 | NA		| ID1| h2 | settled		| ID1| h3 | inError             | update(SETTLED)

type certTestData struct {
	CertificateID common.Hash
	Height        uint64
	Status        agglayer.CertificateStatus
}

type testCaseData struct {
	name            string
	localCert       *certTestData
	agglayerSettled *certTestData
	agglayerPending *certTestData
	resultError     error
	resultActions   *InitialStatusResult
}

// ID |LOCAL			    | AGGLAYER SETTLED		| AGGLAYER PENDING			    | ACTION
//
//	 |--------------------------------------------------------------------------------------------------
//	 |ID | h  | st        | ID | h  | st			| ID | h  | st   			    |
//	 |-------------------------------------------------------------------------------------------------
//	1|N/A 				| ID1| h1 | NA	 		| ID2| h1   | !=inError  		| Agglayer incosistence
//	2|N/A 				| ID1| h2 | NA	 		| ID2| h1   | !=inError  		| Agglayer incosistence???
//	3|nil 				| nil 					| ID1| >h0  | !=inError  		| Agglayer incosistence???
//	4|ID1| h1 | Inerror   | nil 					| nil 							| AggSender incosistence
//	5|ID1| h1 | Settled    | nil 					| nil 							| AggSender incosistence
//	6|ID1| h3 | NA		| NA 					| ID2| h2   | N/A  				| AggSender incosistence
//	7|ID1| h3 | NA		| ID2 | h2 |NA			| NA  							| AggSender incosistence
//	8|ID2| h2 | NA		| ID1| h3 | N/A			| ID3| h4   | !=inError			| AggSender incosistence (2cert jump)
//	9|ID2| h2 | NA		| ID1| h3 | N/A			| ID3| h4   | inError			| store(SETTLED) we ignore pending inError
func TestInitialStateInconsistence(t *testing.T) {
	hash1 := common.HexToHash("0xdead")
	hash2 := common.HexToHash("0xbeef")
	//hash3 := common.HexToHash("0xfeed")

	tests := []testCaseData{
		{
			name:            "1| local:NA 				| settled: ID1| h1 | NA	 		| pending:ID2| h1   | !=inError | Agglayer incosistence",
			localCert:       nil,
			agglayerSettled: &certTestData{hash1, 1, agglayer.Proven},
			agglayerPending: &certTestData{hash2, 1, agglayer.Pending},
			resultError:     ErrorAgglayerInconsistence,
		},
		{
			name:            "2| N/A 				| ID1| h2 | NA	 		| ID2| h1   | !=inError | Agglayer incosistence???",
			localCert:       nil,
			agglayerSettled: &certTestData{hash1, 2, agglayer.Proven},
			agglayerPending: &certTestData{hash2, 1, agglayer.Pending},
			resultError:     ErrorAgglayerInconsistence,
		},
		{
			name:            "3|nil 				| nil 					| ID1| >h0  | !=inError  		| Agglayer incosistence???",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 1, agglayer.Pending},
			resultError:     ErrorAgglayerInconsistence,
		},
		{
			name:            "4|ID1| h1 | Inerror   | nil 					| nil 							| AggSender incosistence",
			localCert:       &certTestData{hash1, 2, agglayer.InError},
			agglayerSettled: nil,
			agglayerPending: nil,
			resultError:     ErrorMismatchStateAgglayerAndLocal,
		},
		{
			name:            "5|ID1| h1 | Settled    | nil 					| nil 							| AggSender incosistence",
			localCert:       &certTestData{hash1, 2, agglayer.Settled},
			agglayerSettled: nil,
			agglayerPending: nil,
			resultError:     ErrorMismatchStateAgglayerAndLocal,
		},
	}
	runTestCases(t, tests)
}

func runTestCases(t *testing.T, tests []testCaseData) {
	t.Helper()
	logger := log.WithFields("module", "unit-test")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			sut := InitialStatus{log: logger}
			if tt.localCert != nil {
				sut.LocalCert = &types.CertificateInfo{
					CertificateID: tt.localCert.CertificateID,
					Height:        tt.localCert.Height,
					Status:        tt.localCert.Status,
				}
			}
			if tt.agglayerSettled != nil {
				sut.AggLayerLastSettledCert = &agglayer.CertificateHeader{
					CertificateID: tt.agglayerSettled.CertificateID,
					Height:        tt.agglayerSettled.Height,
					Status:        tt.agglayerSettled.Status,
				}
			}
			if tt.agglayerPending != nil {
				sut.AggLayerLastPendingCert = &agglayer.CertificateHeader{
					CertificateID: tt.agglayerPending.CertificateID,
					Height:        tt.agglayerPending.Height,
					Status:        tt.agglayerPending.Status,
				}
			}

			actions, err := sut.Process()
			if tt.resultError != nil {
				require.Error(t, err)
				require.Nil(t, actions)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
