package multidownloader

import (
	"fmt"
	"sync"

	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	"github.com/ethereum/go-ethereum/common"
)

type EVMMultidownloaderDebug struct {
	mutexDebug                 sync.Mutex
	debugStepForcedReturnError error
}

func NewEVMMultidownloaderDebug() *EVMMultidownloaderDebug {
	return &EVMMultidownloaderDebug{}
}

func (dh *EVMMultidownloaderDebug) ForceRorg(mismatchingBlockNumber uint64) {
	if dh == nil {
		return
	}
	dh.mutexDebug.Lock()
	defer dh.mutexDebug.Unlock()
	dh.debugStepForcedReturnError = mdrtypes.NewDetectedReorgError(
		mismatchingBlockNumber,
		mdrtypes.ReorgDetectionReason_BlockHashMismatch,
		common.Hash{},
		common.Hash{},
		fmt.Sprintf("ForceRorg: forced reorg at block number %d", mismatchingBlockNumber),
	)
}

func (dh *EVMMultidownloaderDebug) GetInjectedStartStepError() error {
	if dh == nil {
		return nil
	}
	dh.mutexDebug.Lock()
	defer dh.mutexDebug.Unlock()
	if dh.debugStepForcedReturnError != nil {
		err := dh.debugStepForcedReturnError
		dh.debugStepForcedReturnError = nil
		return err
	}
	return nil
}
