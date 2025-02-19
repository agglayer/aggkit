package aggsender

import (
	"fmt"
	"testing"

	agglayerTypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

type certTestData struct {
	CertificateID common.Hash
	Height        uint64
	Status        agglayerTypes.CertificateStatus
}

type initialStateResultTest struct {
	action InitialStatusAction
	subMsg string
	cert   *certTestData
}

type testCaseData struct {
	name            string
	localCert       *certTestData
	agglayerSettled *certTestData
	agglayerPending *certTestData
	resultError     bool
	resultActions   *initialStateResultTest
}

// ID|LOCAL			    | AGGLAYER SETTLED		| AGGLAYER PENDING			    | ACTION
//	 |-------------------------------------------------------------------------------------------------
//	 |ID | h  | st      | ID | h  | st		    | ID | h  | st   			    |
//	 |-------------------------------------------------------------------------------------------------
//	1|N/A 				 | ID1, h1 , NA	 		| ID2, h1   , !=inError  		| Agglayer incosistence
//	2|N/A 				 | ID1, h2 , NA	 		| ID2, h1   , !=inError  		| Agglayer incosistence
//	3|nil 				 | nil 					| ID1, >h0  , !=inError  		| Agglayer incosistence
//	4|ID1, h1 , Inerror  | nil 					| nil 							| AggSender incosistence
//	5|ID1, h1 , Settled  | nil 					| nil 							| AggSender incosistence
//  6|ID1, h1 , !=closed | nil 					| nil 							| incosistence

//	7|ID1, h3 , NA		| NA 					| ID2, h2   , !=InError 		| AggSender incosistence
//	8|ID1, h3 , NA		| ID2, h2 ,NA			| NA  							| AggSender incosistence
//	9|ID2, h2 , NA		| ID1, h3 , N/A			| ID3, h4   , !=inError			| AggSender incosistence (2cert jump)
// 10|ID2, h2 , NA		| ID1, h3 , N/A			| ID3, h4   , inError			| AggSender incosistence (2cert jump)

func TestInitialStateInconsistence(t *testing.T) {
	hash1 := common.HexToHash("0xdead")
	hash2 := common.HexToHash("0xbeef")

	tests := []testCaseData{
		{
			name:            "1|N/A 				 | ID1, h1 , NA	 		| ID2, h1   , !=inError  		| Agglayer incosistence",
			localCert:       nil,
			agglayerSettled: &certTestData{hash1, 1, agglayerTypes.Proven},
			agglayerPending: &certTestData{hash2, 1, agglayerTypes.Pending},
			resultError:     true,
		},
		{
			name:            "2|N/A 				 | ID1, h2 , NA	 		| ID2, h1   , !=inError  		| Agglayer incosistence",
			localCert:       nil,
			agglayerSettled: &certTestData{hash1, 2, agglayerTypes.Proven},
			agglayerPending: &certTestData{hash2, 1, agglayerTypes.Pending},
			resultError:     true,
		},
		{
			name:            "3|nil 				 | nil 					| ID1, >h0  , !=inError  		| Agglayer incosistence",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 1, agglayerTypes.Pending},
			resultError:     true,
		},
		{
			name:            "4|ID1, h1 , Inerror  | nil 					| nil 							| AggSender incosistence",
			localCert:       &certTestData{hash1, 2, agglayerTypes.InError},
			agglayerSettled: nil,
			agglayerPending: nil,
			resultError:     true,
		},
		{
			name:            "5|ID1, h1 , Settled  | nil 					| nil 							| AggSender incosistence",
			localCert:       &certTestData{hash1, 2, agglayerTypes.Settled},
			agglayerSettled: nil,
			agglayerPending: nil,
			resultError:     true,
		},
		{
			name:            "6|ID1, h1 , !=closed | nil 					| nil 							| incosistence",
			localCert:       &certTestData{hash1, 0, agglayerTypes.Proven},
			agglayerSettled: nil,
			agglayerPending: nil,
			resultError:     true,
		},
		{
			name:            "7|ID1, h3 , NA		| NA 					| ID2, h2   , !=InError 		| AggSender incosistence",
			localCert:       &certTestData{hash1, 3, agglayerTypes.Proven},
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash2, 2, agglayerTypes.Proven},
			resultError:     true,
		},
		{
			name:            "8|ID1, h3 , NA		| ID2, h2 ,NA			| NA  							| AggSender incosistence",
			localCert:       &certTestData{hash1, 3, agglayerTypes.Proven},
			agglayerSettled: &certTestData{hash2, 2, agglayerTypes.Proven},
			agglayerPending: nil,
			resultError:     true,
		},
		{
			name:            "9|ID2, h2 , NA		| ID1, h3 , N/A			| ID3, h4   , !=inError			| AggSender incosistence (2cert jump)",
			localCert:       &certTestData{hash1, 2, agglayerTypes.Proven},
			agglayerSettled: &certTestData{hash2, 3, agglayerTypes.Settled},
			agglayerPending: &certTestData{hash2, 4, agglayerTypes.Proven},
			resultError:     true,
		},
		{
			name:            "10|ID2, h2 , NA		| ID1, h3 , N/A			| ID3, h4   , inError			| AggSender incosistence (2cert jump)",
			localCert:       &certTestData{hash1, 2, agglayerTypes.Proven},
			agglayerSettled: &certTestData{hash2, 3, agglayerTypes.Settled},
			agglayerPending: &certTestData{hash2, 4, agglayerTypes.InError},
			resultError:     true,
		},
	}
	runTestCases(t, tests)
}

// ID|LOCAL			    | AGGLAYER SETTLED		| AGGLAYER PENDING			    | ACTION
//
//		 |-------------------------------------------------------------------------------------------------
//		 |ID , h  , st      | ID , h  , st		    | ID , h  , st   			    |
//		 |-------------------------------------------------------------------------------------------------
//		 1| nil 				| nil 					| nil 		   					| none
//		 2| nil 				| nil 					| ID1, h0  , inError  			| store(PENDING) h0 so is next cert
//		 3| nil 				| nil 					| ID1, h1  , inError  			| none
//		 4| nil 				| nil 					| ID1, h0  , !=inError  		| store(PENDING) h0 so is next cert
//		 5| nil 				| nil 					| ID1, h1  , !=inError  		| wait, h1 is not next cert but we wait until pass to inError
//		 6| nil 				| ID1, h1 , NA	 		| nil 							| store(SETTLE)
//		 7| nil 				| ID1, h1 , NA	 		| ID2, h2  , inError  			| store(PENDING)
//		 8| nil 				| ID1, h1 , NA	 		| ID2, h2  , !=inError  		| store(PENDING) h2 is next to h1
//		 9|ID1, h1 , NA		    | nil 					| ID1, h1  , inError  			| update(PENDING)
//		10|ID2, h2 , NA			| ID1, h1 , N/A			| ID2, h2  , N/A		  		| update(PENDING)
//	 11|ID2, h2 , NA		| ID1, h3 , N/A			| nil               			|  store(SETTLED)
//	 12|ID2, h2 , NA		| ID1, h2 , settled		| ID1, h3 , !=inError           |  store(PENDING)
//	 13|ID2, h2 , NA		| ID1, h2 , settled		| ID1, h3 , inError             | store(PENDING)
func TestRegularCases(t *testing.T) {
	hash1 := common.HexToHash("0xdead")
	hash2 := common.HexToHash("0xbeef")

	tests := []testCaseData{
		{
			name:            "01| nil 				| nil 					| nil 		   					| none",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: nil,
			resultActions:   &initialStateResultTest{InitialStatusActionNone, "", nil},
		},
		{
			name:            "02| nil 				| nil 					| ID1, h0  , inError		|store(PENDING) h0 so is next cert",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 0, agglayerTypes.InError},
			resultActions:   &initialStateResultTest{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 0, agglayerTypes.InError}},
		},
		{
			name:            "03| nil 				| nil 					| ID1, h1  , inError   			|none",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 1, agglayerTypes.InError},
			resultActions:   &initialStateResultTest{InitialStatusActionNone, "", nil},
		},
		{
			name:            "04| nil 				| nil 					| ID1, h0  , !=inError  		| store(PENDING) h0 so is next cert",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 0, agglayerTypes.Proven},
			resultActions:   &initialStateResultTest{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 0, agglayerTypes.Proven}},
		},
		{
			name:            "05| nil 				| nil 					| ID1, h1  , !=inError  		| wait, h1 is not next cert but we wait until pass to inError",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 1, agglayerTypes.Proven},
			resultError:     true,
		},
		{
			name:            "06| nil 				| ID1, h1 , NA	 		| nil 							| store(SETTLE)",
			localCert:       nil,
			agglayerSettled: &certTestData{hash1, 1, agglayerTypes.Proven},
			agglayerPending: nil,
			resultActions:   &initialStateResultTest{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 1, agglayerTypes.Proven}},
		},
		{
			name:            "07| nil 				| ID1, h1 , NA	 		| ID2, h2  , inError  			| store(PENDING)",
			localCert:       nil,
			agglayerSettled: &certTestData{hash1, 1, agglayerTypes.Proven},
			agglayerPending: &certTestData{hash2, 2, agglayerTypes.InError},
			resultActions:   &initialStateResultTest{InitialStatusActionInsertNewCert, "", &certTestData{hash2, 2, agglayerTypes.InError}},
		},
		{
			name:            "08| nil 				| ID1, h1 , NA	 		| ID2, h2  , !=inError  		| store(PENDING) h2 is next to h1",
			localCert:       nil,
			agglayerSettled: &certTestData{hash1, 1, agglayerTypes.Settled},
			agglayerPending: &certTestData{hash2, 2, agglayerTypes.Pending},
			resultActions:   &initialStateResultTest{InitialStatusActionInsertNewCert, "", &certTestData{hash2, 2, agglayerTypes.Pending}},
		},
		{
			name:            "09|ID1, h1 , NA		    | nil 					| ID1, h1  , inError  			| update(PENDING)",
			localCert:       &certTestData{hash1, 1, agglayerTypes.Proven},
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 1, agglayerTypes.InError},
			resultActions:   &initialStateResultTest{InitialStatusActionUpdateCurrentCert, "", &certTestData{hash1, 1, agglayerTypes.InError}},
		},

		{
			name:            "10|ID2, h2 , NA			| ID1, h1 , N/A			| ID2, h2  , N/A		  		| update(PENDING)",
			localCert:       &certTestData{hash2, 2, agglayerTypes.Proven},
			agglayerSettled: &certTestData{hash1, 1, agglayerTypes.Settled},
			agglayerPending: &certTestData{hash2, 2, agglayerTypes.InError},
			resultActions:   &initialStateResultTest{InitialStatusActionUpdateCurrentCert, "", &certTestData{hash2, 2, agglayerTypes.InError}},
		},
		{
			name:            "11|ID2, h2 , NA		| ID1, h3 , N/A			| nil               			|  store(SETTLED)",
			localCert:       &certTestData{hash2, 2, agglayerTypes.Proven},
			agglayerSettled: &certTestData{hash1, 3, agglayerTypes.Proven},
			agglayerPending: nil,
			resultActions:   &initialStateResultTest{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 3, agglayerTypes.Proven}},
		},
		{
			name:            "12|ID2, h2 , NA		| ID1, h2 , settled		| ID1, h3 , !=inError           |  store(PENDING)",
			localCert:       &certTestData{hash2, 2, agglayerTypes.Proven},
			agglayerSettled: &certTestData{hash1, 2, agglayerTypes.Settled},
			agglayerPending: &certTestData{hash1, 3, agglayerTypes.Proven},
			resultActions:   &initialStateResultTest{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 3, agglayerTypes.Proven}},
		},
		{
			name:            "13|ID2, h2 , NA		| ID1, h2 , settled		| ID1, h3 , inError             | store(PENDING)",
			localCert:       &certTestData{hash2, 2, agglayerTypes.Proven},
			agglayerSettled: &certTestData{hash1, 2, agglayerTypes.Settled},
			agglayerPending: &certTestData{hash1, 3, agglayerTypes.InError},
			resultActions:   &initialStateResultTest{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 3, agglayerTypes.InError}},
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
				sut.SettledCert = &agglayerTypes.CertificateHeader{
					CertificateID: tt.agglayerSettled.CertificateID,
					Height:        tt.agglayerSettled.Height,
					Status:        tt.agglayerSettled.Status,
				}
			}
			if tt.agglayerPending != nil {
				sut.PendingCert = &agglayerTypes.CertificateHeader{
					CertificateID: tt.agglayerPending.CertificateID,
					Height:        tt.agglayerPending.Height,
					Status:        tt.agglayerPending.Status,
				}
			}

			action, err := sut.Process()
			if tt.resultError {
				require.Error(t, err)
				require.Nil(t, action)
			} else {
				require.NoError(t, err)
				if tt.resultActions != nil {
					fmt.Print("test:", tt.name)
					fmt.Print("result:", action.String())
					require.Equal(t, tt.resultActions.action, action.Action)
					require.Contains(t, action.Message, tt.resultActions.subMsg)
					if tt.resultActions.cert != nil {
						require.NotNil(t, action.Cert)
						require.Equal(t, tt.resultActions.cert.CertificateID, action.Cert.CertificateID)
						require.Equal(t, tt.resultActions.cert.Height, action.Cert.Height)
						require.Equal(t, tt.resultActions.cert.Status, action.Cert.Status)
					}
				}
			}
		})
	}
}
