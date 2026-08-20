package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCORSConfigOriginAllowed(t *testing.T) {
	tests := []struct {
		name   string
		cors   CORSConfig
		origin string
		want   bool
	}{
		{
			name:   "disabled allows any origin, matching the pre-CORS-config default",
			cors:   CORSConfig{Enabled: false, AllowedOrigins: []string{"https://allowed.com"}},
			origin: "https://not-in-the-list.com",
			want:   true,
		},
		{
			name:   "disabled allows an empty origin too",
			cors:   CORSConfig{Enabled: false},
			origin: "",
			want:   true,
		},
		{
			name:   "enabled with empty AllowedOrigins grants nothing",
			cors:   CORSConfig{Enabled: true},
			origin: "https://example.com",
			want:   false,
		},
		{
			name:   "enabled with wildcard allows any origin",
			cors:   CORSConfig{Enabled: true, AllowedOrigins: []string{"*"}},
			origin: "https://example.com",
			want:   true,
		},
		{
			name:   "enabled with exact match allows it",
			cors:   CORSConfig{Enabled: true, AllowedOrigins: []string{"https://example.com"}},
			origin: "https://example.com",
			want:   true,
		},
		{
			name:   "enabled matching is case-insensitive",
			cors:   CORSConfig{Enabled: true, AllowedOrigins: []string{"HTTPS://EXAMPLE.COM"}},
			origin: "https://example.com",
			want:   true,
		},
		{
			name:   "enabled rejects an origin not in the list",
			cors:   CORSConfig{Enabled: true, AllowedOrigins: []string{"https://example.com"}},
			origin: "https://not-allowed.com",
			want:   false,
		},
		{
			name:   "enabled with a single embedded wildcard matches a subdomain",
			cors:   CORSConfig{Enabled: true, AllowedOrigins: []string{"https://*.example.com"}},
			origin: "https://app.example.com",
			want:   true,
		},
		{
			name:   "enabled with a single embedded wildcard rejects a non-matching origin",
			cors:   CORSConfig{Enabled: true, AllowedOrigins: []string{"https://*.example.com"}},
			origin: "https://example.org",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.cors.OriginAllowed(tt.origin))
		})
	}
}
