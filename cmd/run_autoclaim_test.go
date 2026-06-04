package main

import (
	"testing"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/stretchr/testify/require"
)

func TestShouldRunAutoClaimRequiresComponentAndEnabledConfig(t *testing.T) {
	require.False(t, shouldRunAutoClaim([]string{aggkitcommon.AUTOCLAIM}, false))
	require.False(t, shouldRunAutoClaim([]string{aggkitcommon.BRIDGE}, true))
	require.True(t, shouldRunAutoClaim([]string{aggkitcommon.AUTOCLAIM}, true))
}

func TestAutoClaimSelectsOnlyL1RuntimeDependencies(t *testing.T) {
	components := []string{aggkitcommon.AUTOCLAIM}

	require.True(t, l1InfoTreeMustRun(components))
	require.True(t, isNeeded([]string{aggkitcommon.L1BRIDGESYNC, aggkitcommon.AUTOCLAIM}, components))
	require.False(t, isNeeded([]string{aggkitcommon.AGGSENDER, aggkitcommon.AGGORACLE}, components))
	require.False(t, isNeeded([]string{aggkitcommon.L2BRIDGESYNC, aggkitcommon.L2CLAIMSYNC}, components))
}
