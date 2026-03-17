package types

import (
	"fmt"
	"strings"
)

// TrueFalseAutoMode represents a tri-state config value: true, false, or auto.
// Mode is set from the config file via UnmarshalText; Resolved is set programmatically.
type TrueFalseAutoMode struct {
	Mode     string `mapstructure:"-"`
	Resolved *bool  `mapstructure:"-"`
}

const (
	trueModeStr  = "true"
	falseModeStr = "false"
	autoModeStr  = "auto"
)

var (
	// TrueMode always activates the feature.
	TrueMode = TrueFalseAutoMode{Mode: trueModeStr, Resolved: func() *bool { b := true; return &b }()}
	// FalseMode always deactivates the feature.
	FalseMode = TrueFalseAutoMode{Mode: falseModeStr, Resolved: func() *bool { b := false; return &b }()}
	// AutoMode decides automatically based on context.
	AutoMode = TrueFalseAutoMode{Mode: autoModeStr}
)

// UnmarshalText implements encoding.TextUnmarshaler.
func (m *TrueFalseAutoMode) UnmarshalText(text []byte) error {
	str := strings.ToLower(strings.TrimSpace(string(text)))
	switch str {
	case trueModeStr:
		m.Mode = trueModeStr
	case falseModeStr:
		m.Mode = falseModeStr
	case autoModeStr:
		m.Mode = autoModeStr
	default:
		return fmt.Errorf("invalid TrueFalseAutoMode: %s (valid values: true, false, auto)", str)
	}
	return nil
}

// String returns the string representation.
func (m TrueFalseAutoMode) String() string {
	if m.Resolved != nil {
		return fmt.Sprintf("{Mode: %s, Resolved: %t}", m.Mode, *m.Resolved)
	} else {
		return fmt.Sprintf("{Mode: %s, Resolved: <not yet resolved>}", m.Mode)
	}
}

// Validate checks that the mode is a valid value. Empty mode is allowed.
func (m TrueFalseAutoMode) Validate(fieldName string) error {
	if m.Mode == "" {
		return nil
	}
	var cpy TrueFalseAutoMode
	if err := cpy.UnmarshalText([]byte(m.Mode)); err != nil {
		return fmt.Errorf("invalid %s configuration: %w", fieldName, err)
	}
	return nil
}

// Resolve converts the mode to a boolean using autoModeResult for AutoMode,
// stores the result in Resolved, and returns it.
func (m *TrueFalseAutoMode) Resolve(autoModeResult bool) bool {
	var result bool
	switch m.Mode {
	case trueModeStr:
		result = true
	case falseModeStr:
		result = false
	case autoModeStr:
		result = autoModeResult
	}
	m.Resolved = &result
	return result
}
