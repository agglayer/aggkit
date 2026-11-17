package l1infotreesync_test

import (
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
)

// fastRetryAfterError is the multidownloader RetryAfterError policy for tests on a simulated chain: a step that
// fails while the chain is being forked is retried almost at once, so the tests don't depend on the (much
// longer) production delays
func fastRetryAfterError() aggkitcommon.RetryPolicyGenericConfig {
	return aggkitcommon.RetryPolicyGenericConfig{
		Mode:       aggkitcommon.RetryConfigModeDelays,
		MaxRetries: aggkitcommon.MaxAttemptsInfinite,
		Delays:     []types.Duration{types.NewDuration(10 * time.Millisecond)},
	}
}
