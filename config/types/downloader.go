package types

import (
	"fmt"
	"strings"
)

// Downloader selects the block download backend used by a synchronizer.
//
// The zero value ("") is equivalent to LegacyDownloader, so a configuration that does not set the
// field keeps the historical behavior.
type Downloader string

const (
	// LegacyDownloader uses the dedicated EVM downloader built into each synchronizer.
	LegacyDownloader Downloader = "legacy"
	// Multidownloader uses the shared multidownloader service.
	Multidownloader Downloader = "multidownloader"
)

// SupportedDownloaders lists every valid Downloader value.
var SupportedDownloaders = []Downloader{LegacyDownloader, Multidownloader}

// UnmarshalText implements encoding.TextUnmarshaler. It is case-insensitive and trims spaces.
// The empty value is accepted and behaves as LegacyDownloader.
func (d *Downloader) UnmarshalText(text []byte) error {
	parsed := Downloader(strings.ToLower(strings.TrimSpace(string(text))))
	if err := parsed.Validate(); err != nil {
		return err
	}
	if parsed == "" {
		parsed = LegacyDownloader
	}
	*d = parsed
	return nil
}

// String returns the downloader as a string.
func (d Downloader) String() string {
	return string(d)
}

// IsMultidownloader reports whether the shared multidownloader is selected.
// The zero value selects the legacy downloader.
func (d Downloader) IsMultidownloader() bool {
	return d == Multidownloader
}

// IsLegacy reports whether the legacy downloader is selected. The zero value is legacy.
func (d Downloader) IsLegacy() bool {
	return !d.IsMultidownloader()
}

// Validate checks that the downloader is a supported value. The empty value is valid and behaves as
// LegacyDownloader.
func (d Downloader) Validate() error {
	switch d {
	case "", LegacyDownloader, Multidownloader:
		return nil
	default:
		return fmt.Errorf("invalid Downloader %q (supported values: %s, %s)",
			d, LegacyDownloader, Multidownloader)
	}
}
