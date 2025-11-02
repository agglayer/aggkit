package multidownloader

import (
	"sync"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
)

type Statistics struct {
	mutex sync.Mutex

	timeTrackerTotal    aggkitcommon.TimeTracker
	timeTrackerEthCalls aggkitcommon.TimeTracker
	timeTrackerDB       aggkitcommon.TimeTracker
	totalLogsSynced     uint64
	totalBlocksSynced   uint64
}

func NewStatistics() *Statistics {
	return &Statistics{}
}
func (s *Statistics) StartSyncing() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.timeTrackerTotal.Start()
}
func (s *Statistics) FinishSyncing() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.timeTrackerTotal.Stop()
}

func (s *Statistics) LaunchedEthCall() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.timeTrackerEthCalls.Start()
}

func (s *Statistics) StartDBOperation() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.timeTrackerDB.Start()
}

func (s *Statistics) FinishDBOperation(_ error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.timeTrackerDB.Stop()
}

func (s *Statistics) ETA(pendingBlocks uint64) time.Duration {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.totalBlocksSynced == 0 {
		return 0
	}
	elapsed := s.timeTrackerTotal.Elapsed()
	estimatedPendingTime := time.Duration(float64(elapsed) * (float64(pendingBlocks) / float64(s.totalBlocksSynced)))
	return estimatedPendingTime
}

func (s *Statistics) FinishEthCall(err error, numLogs uint64, numBlocks uint64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.timeTrackerEthCalls.Stop()
	if err == nil {
		s.totalLogsSynced += numLogs
		s.totalBlocksSynced += numBlocks
	}
}

func (s *Statistics) Show(logFunc func(format string, args ...interface{}), iteration int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	logFunc("[%d]Historical/Step: time Total=%s", iteration, s.timeTrackerTotal.String())
	logFunc("-----------------------------------------------------------------------")
	logFunc("[%d]Historical/Step: time EthCalls=%s", iteration, s.timeTrackerEthCalls.String())
	logFunc("[%d]Historical/Step: time Database=%s", iteration, s.timeTrackerDB.String())
	logFunc("[%d]Historical/Step: totalLogsSynced=%d", iteration, s.totalLogsSynced)
	logFunc("[%d]Historical/Step: totalBlocksSynced=%d", iteration, s.totalBlocksSynced)

}
