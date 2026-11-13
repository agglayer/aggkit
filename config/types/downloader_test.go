package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDownloader_UnmarshalText(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expected      Downloader
		expectedError string
	}{
		{name: "legacy lowercase", input: "legacy", expected: LegacyDownloader},
		{name: "legacy uppercase", input: "LEGACY", expected: LegacyDownloader},
		{name: "legacy mixed case", input: "LeGaCy", expected: LegacyDownloader},
		{name: "legacy with whitespace", input: "  legacy  ", expected: LegacyDownloader},
		{name: "multidownloader lowercase", input: "multidownloader", expected: Multidownloader},
		{name: "multidownloader uppercase", input: "MULTIDOWNLOADER", expected: Multidownloader},
		{name: "multidownloader mixed case", input: "MultiDownloader", expected: Multidownloader},
		{name: "multidownloader with whitespace", input: "  multidownloader  ", expected: Multidownloader},
		{name: "empty means legacy", input: "", expected: LegacyDownloader},
		{name: "blank means legacy", input: "   ", expected: LegacyDownloader},
		{
			name:          "invalid value",
			input:         "invalid",
			expectedError: "invalid Downloader \"invalid\" (supported values: legacy, multidownloader)",
		},
		{
			name:          "numeric value",
			input:         "1",
			expectedError: "invalid Downloader \"1\" (supported values: legacy, multidownloader)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Downloader
			err := d.UnmarshalText([]byte(tt.input))

			if tt.expectedError == "" {
				require.NoError(t, err)
				require.Equal(t, tt.expected, d)
			} else {
				require.Error(t, err)
				require.Equal(t, tt.expectedError, err.Error())
			}
		})
	}
}

func TestDownloader_String(t *testing.T) {
	tests := []struct {
		name       string
		downloader Downloader
		expected   string
	}{
		{name: "legacy", downloader: LegacyDownloader, expected: "legacy"},
		{name: "multidownloader", downloader: Multidownloader, expected: "multidownloader"},
		{name: "zero value", downloader: Downloader(""), expected: ""},
		{name: "invalid", downloader: Downloader("invalid"), expected: "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.downloader.String())
		})
	}
}

func TestDownloader_IsMultidownloader(t *testing.T) {
	tests := []struct {
		name       string
		downloader Downloader
		expected   bool
	}{
		{name: "legacy", downloader: LegacyDownloader, expected: false},
		{name: "multidownloader", downloader: Multidownloader, expected: true},
		{name: "zero value behaves legacy", downloader: Downloader(""), expected: false},
		{name: "empty string behaves legacy", downloader: Downloader(""), expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.downloader.IsMultidownloader())
			require.Equal(t, !tt.expected, tt.downloader.IsLegacy())
		})
	}
}

func TestDownloader_Validate(t *testing.T) {
	tests := []struct {
		name          string
		downloader    Downloader
		expectedError string
	}{
		{name: "legacy", downloader: LegacyDownloader},
		{name: "multidownloader", downloader: Multidownloader},
		{name: "zero value is valid", downloader: Downloader("")},
		{
			name:          "invalid value",
			downloader:    Downloader("invalid"),
			expectedError: "invalid Downloader \"invalid\" (supported values: legacy, multidownloader)",
		},
		{
			name:          "uppercase is invalid until unmarshaled",
			downloader:    Downloader("LEGACY"),
			expectedError: "invalid Downloader \"LEGACY\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.downloader.Validate()
			if tt.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}
