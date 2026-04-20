package statuschecker

import (
	"fmt"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

type certTestData struct {
	CertificateID common.Hash
	Height        uint64
	Status        agglayertypes.CertificateStatus
}

type initialStateResultTest struct {
	action initialStatusAction
	subMsg string
	cert   *certTestData
}

type testCaseData struct {
	name             string
	localCert        *certTestData
	localSettledCert *certTestData
	agglayerSettled  *certTestData
	agglayerPending  *certTestData
	resultError      bool
	errorContains    string
	resultActions    []*initialStateResultTest
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
//
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
			agglayerSettled: &certTestData{hash1, 1, agglayertypes.Proven},
			agglayerPending: &certTestData{hash2, 1, agglayertypes.Pending},
			resultError:     true,
		},
		{
			name:            "2|N/A 				 | ID1, h2 , NA	 		| ID2, h1   , !=inError  		| Agglayer incosistence",
			localCert:       nil,
			agglayerSettled: &certTestData{hash1, 2, agglayertypes.Proven},
			agglayerPending: &certTestData{hash2, 1, agglayertypes.Pending},
			resultError:     true,
		},
		{
			name:            "3|nil 				 | nil 					| ID1, >h0  , !=inError  		| Agglayer incosistence",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 1, agglayertypes.Pending},
			resultError:     true,
		},
		{
			name:            "4|ID1, h1 , Inerror  | nil 					| nil 							| AggSender incosistence",
			localCert:       &certTestData{hash1, 2, agglayertypes.InError},
			agglayerSettled: nil,
			agglayerPending: nil,
			resultError:     true,
			errorContains:   "Manual recovery required: wipe the aggsender DB and restart aggsender",
		},
		{
			name:            "5|ID1, h1 , Settled  | nil 					| nil 							| AggSender incosistence",
			localCert:       &certTestData{hash1, 2, agglayertypes.Settled},
			agglayerSettled: nil,
			agglayerPending: nil,
			resultError:     true,
			errorContains:   "Manual recovery required: wipe the aggsender DB and restart aggsender",
		},
		{
			name:            "6|ID1, h1 , !=closed | nil 					| nil 							| incosistence",
			localCert:       &certTestData{hash1, 0, agglayertypes.Proven},
			agglayerSettled: nil,
			agglayerPending: nil,
			resultError:     true,
			errorContains:   "Manual recovery required: wipe the aggsender DB and restart aggsender",
		},
		{
			name:            "7|ID1, h3 , NA		| NA 					| ID2, h2   , !=InError 		| AggSender incosistence",
			localCert:       &certTestData{hash1, 3, agglayertypes.Proven},
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash2, 2, agglayertypes.Proven},
			resultError:     true,
		},
		{
			name:            "8|ID1, h3 , NA		| ID2, h2 ,NA			| NA  							| AggSender incosistence",
			localCert:       &certTestData{hash1, 3, agglayertypes.Proven},
			agglayerSettled: &certTestData{hash2, 2, agglayertypes.Proven},
			agglayerPending: nil,
			resultError:     true,
			errorContains:   "Manual recovery required: wipe the aggsender DB and restart aggsender",
		},
		{
			name:            "9|ID2, h2 , NA		| ID1, h3 , N/A			| ID3, h4   , !=inError			| AggSender incosistence (2cert jump)",
			localCert:       &certTestData{hash1, 2, agglayertypes.Proven},
			agglayerSettled: &certTestData{hash2, 3, agglayertypes.Settled},
			agglayerPending: &certTestData{hash2, 4, agglayertypes.Proven},
			resultError:     true,
			errorContains:   "Manual recovery required: wipe the aggsender DB and restart aggsender",
		},
		{
			name:            "10|ID2, h2 , NA		| ID1, h3 , N/A			| ID3, h4   , inError			| AggSender incosistence (2cert jump)",
			localCert:       &certTestData{hash1, 2, agglayertypes.Proven},
			agglayerSettled: &certTestData{hash2, 3, agglayertypes.Settled},
			agglayerPending: &certTestData{hash2, 4, agglayertypes.InError},
			resultError:     true,
			errorContains:   "Manual recovery required: wipe the aggsender DB and restart aggsender",
		},
	}
	runTestCases(t, tests)
}

// ID|LOCAL			    | AGGLAYER SETTLED		| AGGLAYER PENDING			    | ACTION
//
//		 |-------------------------------------------------------------------------------------------------
//		 |ID , h  , st      | ID , h  | st		    | ID , h  | st   			    |
//		 |-------------------------------------------------------------------------------------------------
//		 1| nil 				| nil 					| nil 		   					| none
//		 2| nil 				| nil 					| ID1, h0  , inError  			| store(PENDING) h0 so is next cert
//		 3| nil 				| nil 					| ID1, h1  , inError  			| none
//		 4| nil 				| nil 					| ID1, h0  , !=inError  		| store(PENDING) h0 so is next cert
//		 5| nil 				| nil 					| ID1, h1  , !=inError  		| wait, h1 is not next cert but we wait until pass to inError
//		 6| nil 				| ID1, h1 , NA	 		| nil 							| store(SETTLE)
//		 7| nil 				| ID1, h1 , NA	 		| ID2, h2  , inError  			| store(PENDING)
//		 8| nil 				| ID1, h1 , NA	 		| ID2, h2  , !=inError  		| store(PENDING) h2 is next to h1
//		 9|ID1, h1 , NA		| nil 					| ID1, h1  , inError  			| update(PENDING)
//		10|ID2, h2 , NA		| ID1, h1 , N/A			| ID2, h2  , N/A		  		| update(PENDING)
//	 11|ID2, h2 , NA		| ID1, h3 , N/A			| nil               			| store(SETTLED)
//	 12|ID2, h2 , NA		| ID1, h2 , settled		| ID1, h3 , !=inError           | store(PENDING)
//	 13|ID2, h2 , NA		| ID1, h2 , settled		| ID1, h3 , inError             | store(PENDING)
//	 14| LocalCert: ID3, h1, pending	| LocalSettled: ID2, h1	| AgglayerSettled: ID1, h2   | AgglayerPending: ID3, h1 | agglayer doesn't have settled cert
//	 15| LocalCert: ID3, h1, pending	| LocalSettled: ID2, h1 | AgglayerSettled: ID2, h1 | AgglayerPending: ID3, h1 | store(PENDING) & none(SETTLED)
//	 16| LocalCert: ID3, h1, pending	| LocalSettled: ID2, h1	| AgglayerSettled: ID2, h2   | AgglayerPending: ID3, h1 | settled cert ID mismatch
//	 17| LocalCert: ID3, h1, pending	| LocalSettled: ID1, h2	| AgglayerSettled: ID2, h3   | AgglayerPending: ID3, h1 | store(PENDING) & store(SETTLED)
func TestRegularCases(t *testing.T) {
	hash1 := common.HexToHash("0xdead")
	hash2 := common.HexToHash("0xbeef")
	hash3 := common.HexToHash("0xcafe")

	tests := []testCaseData{
		{
			name:            "01| nil 				| nil 					| nil 		   					| none",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: nil,
			resultActions: []*initialStateResultTest{
				{InitialStatusActionNone, "", nil},
			},
		},
		{
			name:            "02| nil 				| nil 					| ID1, h0  , inError		|store(PENDING) h0 so is next cert",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 0, agglayertypes.InError},
			resultActions: []*initialStateResultTest{
				{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 0, agglayertypes.InError}},
				{InitialStatusActionNone, "", nil},
			},
		},
		{
			name:            "03| nil 				| nil 					| ID1, h1  , inError   			|none",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 1, agglayertypes.InError},
			resultActions: []*initialStateResultTest{
				{InitialStatusActionNone, "", nil},
				{InitialStatusActionNone, "", nil},
			},
		},
		{
			name:            "04| nil 				| nil 					| ID1, h0  , !=inError  		| store(PENDING) h0 so is next cert",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 0, agglayertypes.Proven},
			resultActions: []*initialStateResultTest{
				{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 0, agglayertypes.Proven}},
				{InitialStatusActionNone, "", nil},
			},
		},
		{
			name:            "05| nil 				| nil 					| ID1, h1  , !=inError  		| wait, h1 is not next cert but we wait until pass to inError",
			localCert:       nil,
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 1, agglayertypes.Proven},
			resultError:     true,
		},
		{
			name:            "06| nil 				| ID1, h1 , NA	 		| nil 							| store(SETTLE)",
			localCert:       nil,
			agglayerSettled: &certTestData{hash1, 1, agglayertypes.Proven},
			agglayerPending: nil,
			resultActions: []*initialStateResultTest{
				{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 1, agglayertypes.Proven}},
			},
		},
		{
			name:            "07| nil 				| ID1, h1 , NA	 		| ID2, h2  , inError  			| store(PENDING)&store(SETTLED)",
			localCert:       nil,
			agglayerSettled: &certTestData{hash1, 1, agglayertypes.Proven},
			agglayerPending: &certTestData{hash2, 2, agglayertypes.InError},
			resultActions: []*initialStateResultTest{
				{InitialStatusActionInsertNewCert, "", &certTestData{hash2, 2, agglayertypes.InError}},
				{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 1, agglayertypes.Proven}},
			},
		},
		{
			name:            "08| nil 				| ID1, h1 , NA	 		| ID2, h2  , !=inError  		| store(PENDING) h2 is next to h1 & store(SETTLED)",
			localCert:       nil,
			agglayerSettled: &certTestData{hash1, 1, agglayertypes.Settled},
			agglayerPending: &certTestData{hash2, 2, agglayertypes.Pending},
			resultActions: []*initialStateResultTest{
				{InitialStatusActionInsertNewCert, "", &certTestData{hash2, 2, agglayertypes.Pending}},
				{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 1, agglayertypes.Settled}},
			},
		},
		{
			name:            "09|ID1, h1 , NA		| nil 					| ID1, h1  , inError  			| update(PENDING)",
			localCert:       &certTestData{hash1, 1, agglayertypes.Proven},
			agglayerSettled: nil,
			agglayerPending: &certTestData{hash1, 1, agglayertypes.InError},
			resultActions: []*initialStateResultTest{
				{InitialStatusActionUpdateCurrentCert, "", &certTestData{hash1, 1, agglayertypes.InError}},
				{InitialStatusActionNone, "", nil},
			},
		},
		{
			name:            "10|ID2, h2 , NA		| ID1, h1 , N/A			| ID2, h2  , N/A		  		| update(PENDING)&store(SETTLED)",
			localCert:       &certTestData{hash2, 2, agglayertypes.Proven},
			agglayerSettled: &certTestData{hash1, 1, agglayertypes.Settled},
			agglayerPending: &certTestData{hash2, 2, agglayertypes.InError},
			resultActions: []*initialStateResultTest{
				{InitialStatusActionUpdateCurrentCert, "", &certTestData{hash2, 2, agglayertypes.InError}},
				{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 1, agglayertypes.Settled}},
			},
		},
		{
			name:            "11|ID2, h2 , NA		| ID1, h3 , N/A			| nil               			|  store(SETTLED)",
			localCert:       &certTestData{hash2, 2, agglayertypes.Proven},
			agglayerSettled: &certTestData{hash1, 3, agglayertypes.Proven},
			agglayerPending: nil,
			resultActions:   []*initialStateResultTest{{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 3, agglayertypes.Proven}}},
		},
		{
			name:            "12|ID2, h2 , NA		| ID1, h2 , settled		| ID1, h3 , !=inError           | store(PENDING)&store(SETTLED)",
			localCert:       &certTestData{hash2, 2, agglayertypes.Proven},
			agglayerSettled: &certTestData{hash1, 2, agglayertypes.Settled},
			agglayerPending: &certTestData{hash1, 3, agglayertypes.Proven},
			resultActions: []*initialStateResultTest{
				{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 3, agglayertypes.Proven}},
				{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 2, agglayertypes.Settled}},
			},
		},
		{
			name:             "13|ID2, h2 , NA		| ID1, h2 , settled		| ID1, h3 , inError             | store(PENDING)&store(SETTLED)",
			localCert:        &certTestData{hash2, 2, agglayertypes.Proven},
			localSettledCert: nil,
			agglayerSettled:  &certTestData{hash1, 2, agglayertypes.Settled},
			agglayerPending:  &certTestData{hash1, 3, agglayertypes.InError},
			resultActions: []*initialStateResultTest{
				{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 3, agglayertypes.InError}},
				{InitialStatusActionInsertNewCert, "", &certTestData{hash1, 2, agglayertypes.Settled}},
			},
		},
		{
			name:             "14| LocalCert: ID3, h1, pending	| LocalSettled: ID2, h1	| AgglayerSettled: ID1, h2   | AgglayerPending: ID3, h1 | agglayer doesn't have settled cert",
			localCert:        &certTestData{hash1, 3, agglayertypes.Pending},
			localSettledCert: &certTestData{hash1, 2, agglayertypes.Settled},
			agglayerSettled:  &certTestData{hash2, 1, agglayertypes.Settled},
			agglayerPending:  &certTestData{hash1, 3, agglayertypes.Pending},
			resultError:      true,
			errorContains:    "Manual recovery required: wipe the aggsender DB and restart aggsender",
		},
		{
			name:             "15| LocalCert: ID3, h1, pending	| LocalSettled: ID2, h1 | AgglayerSettled: ID2, h1 | AgglayerPending: ID3, h1 | store(PENDING) & none(SETTLED)",
			localCert:        &certTestData{hash1, 3, agglayertypes.Pending},
			localSettledCert: &certTestData{hash2, 2, agglayertypes.Settled},
			agglayerSettled:  &certTestData{hash2, 2, agglayertypes.Settled},
			agglayerPending:  &certTestData{hash1, 3, agglayertypes.Pending},
			resultActions: []*initialStateResultTest{
				{InitialStatusActionUpdateCurrentCert, "", &certTestData{hash1, 3, agglayertypes.Pending}},
				{InitialStatusActionNone, "", nil},
			},
		},
		{
			name:             "16| LocalCert: ID3, h1, pending	| LocalSettled: ID2, h1	| AgglayerSettled: ID2, h2   | AgglayerPending: ID3, h1 | settled cert ID mismatch",
			localCert:        &certTestData{hash1, 3, agglayertypes.Pending},
			localSettledCert: &certTestData{hash1, 2, agglayertypes.Settled},
			agglayerSettled:  &certTestData{hash2, 2, agglayertypes.Settled},
			agglayerPending:  &certTestData{hash1, 3, agglayertypes.Pending},
			resultError:      true,
			errorContains:    "Manual recovery required: wipe the aggsender DB and restart aggsender",
		},
		{
			name:             "17| LocalCert: ID3, h1, pending	| LocalSettled: ID1, h2	| AgglayerSettled: ID2, h3   | AgglayerPending: ID3, h1 | store(PENDING) & store(SETTLED)",
			localCert:        &certTestData{hash1, 3, agglayertypes.Pending},
			localSettledCert: &certTestData{hash2, 1, agglayertypes.Settled},
			agglayerSettled:  &certTestData{hash3, 2, agglayertypes.Settled},
			agglayerPending:  &certTestData{hash1, 3, agglayertypes.Pending},
			resultActions: []*initialStateResultTest{
				{InitialStatusActionUpdateCurrentCert, "", &certTestData{hash1, 3, agglayertypes.Pending}},
				{InitialStatusActionInsertNewCert, "", &certTestData{hash3, 2, agglayertypes.Settled}},
			},
		},
	}
	runTestCases(t, tests)
}

func runTestCases(t *testing.T, tests []testCaseData) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sut := initialStatus{}
			if tt.localCert != nil {
				sut.LocalLastCert = &types.CertificateHeader{
					CertificateID: tt.localCert.CertificateID,
					Height:        tt.localCert.Height,
					Status:        tt.localCert.Status,
				}
			}
			if tt.agglayerSettled != nil {
				sut.AgglayerLastSettledCert = &agglayertypes.CertificateHeader{
					CertificateID: tt.agglayerSettled.CertificateID,
					Height:        tt.agglayerSettled.Height,
					Status:        tt.agglayerSettled.Status,
				}
			}
			if tt.agglayerPending != nil {
				sut.AgglayerLastPendingCert = &agglayertypes.CertificateHeader{
					CertificateID: tt.agglayerPending.CertificateID,
					Height:        tt.agglayerPending.Height,
					Status:        tt.agglayerPending.Status,
				}
			}
			if tt.localSettledCert != nil {
				sut.LocalLastSettledCert = &types.CertificateHeader{
					CertificateID: tt.localSettledCert.CertificateID,
					Height:        tt.localSettledCert.Height,
					Status:        tt.localSettledCert.Status,
				}
			}

			actions, err := sut.process()
			if tt.resultError {
				require.Error(t, err)
				require.Nil(t, actions)
				if tt.errorContains != "" {
					require.ErrorContains(t, err, tt.errorContains)
				}
			} else {
				require.NoError(t, err)
				if tt.resultActions != nil {
					require.Len(t, actions, len(tt.resultActions))
					for i, resultAction := range tt.resultActions {
						action := actions[i]
						fmt.Print("test:", tt.name)
						fmt.Print("result:", action.String())
						require.Equal(t, resultAction.action, action.action)
						require.Contains(t, action.message, resultAction.subMsg)
						if resultAction.cert != nil {
							require.NotNil(t, action.cert)
							require.Equal(t, resultAction.cert.CertificateID, action.cert.CertificateID)
							require.Equal(t, resultAction.cert.Height, action.cert.Height)
							require.Equal(t, resultAction.cert.Status, action.cert.Status)
						}
					}
				}
			}
		})
	}
}
