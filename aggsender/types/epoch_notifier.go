package types

import (
	"context"
	"fmt"
)

// EpochEvent is the event that notifies the neear end epoch
type EpochEvent struct {
	Epoch uint64
	// ExtraInfo if a detailed information about the epoch that depends on implementation
	ExtraInfo fmt.Stringer
}

type EpochStatus struct {
	Epoch        uint64
	PercentEpoch float64
}

func (e EpochEvent) String() string {
	return fmt.Sprintf("EpochEvent: epoch=%d extra=%s", e.Epoch, e.ExtraInfo)
}

type EpochNotifier interface {
	// NotifyEpochStarted notifies the epoch is close to end.
	Subscribe(id string) <-chan EpochEvent
	// Start starts the notifier synchronously
	Start(ctx context.Context)
	// CheckCanSendCertificate check if the epoch time it's ok to send a certificate
	CheckCanSendCertificate() error
	// String returns a string representation of the epoch notifier
	String() string
}
