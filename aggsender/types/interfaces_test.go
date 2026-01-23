package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHealthCheckResponse_IsHealthy(t *testing.T) {
	resp := &HealthCheckResponse{
		Status: HealthCheckStatusOK,
	}
	if !resp.IsHealthy() {
		t.Errorf("expected IsHealthy to return true for OK status")
	}

	resp = &HealthCheckResponse{
		Status: "NOT_OK",
	}
	if resp.IsHealthy() {
		t.Errorf("expected IsHealthy to return false for non-OK status")
	}

	var nilResp *HealthCheckResponse
	if nilResp.IsHealthy() {
		t.Errorf("expected IsHealthy to return false for nil receiver")
	}
}

func TestHealthCheckResponse_String(t *testing.T) {
	resp := &HealthCheckResponse{
		Status:       HealthCheckStatusOK,
		StatusReason: "All systems go",
		Version:      "1.0.0",
	}
	expected := "HealthCheckResponse{Status: OK, StatusReason: All systems go, Version: 1.0.0}"
	if resp.String() != expected {
		t.Errorf("unexpected String() output: got %q, want %q", resp.String(), expected)
	}

	var nilResp *HealthCheckResponse
	expectedNil := "HealthCheckResponse is nil"
	if nilResp.String() != expectedNil {
		t.Errorf("unexpected String() output for nil receiver: got %q, want %q", nilResp.String(), expectedNil)
	}
}

func TestCertificateSendTriggerMode_String(t *testing.T) {
	var mode CertificateSendTriggerMode = ""
	require.Equal(t, "???", mode.String())
	require.Equal(t, string(NewBridgeTriggerMode), NewBridgeTriggerMode.String())
}
