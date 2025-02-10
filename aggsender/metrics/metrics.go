package metrics

import (
	"github.com/agglayer/aggkit/prometheus"
	prometheusClient "github.com/prometheus/client_golang/prometheus"
)

const (
	prefix                   = "aggsender_"
	numberOfCertificatesSent = prefix + "number_of_certificates_sent"
	numberOfSendingErrors    = prefix + "number_of_sending_errors"
	numberOfSendingRetries   = prefix + "number_of_sending_retries"
	numberOfSendingSuccesses = prefix + "number_of_sending_successes"
	certificateBuildTime     = prefix + "certificate_build_time"
	proverTime               = prefix + "prover_time"
)

// Register the metrics for the aggsender package
func Register() {
	gauges := []prometheusClient.GaugeOpts{
		{
			Name: numberOfCertificatesSent,
			Help: "[AGGSENDER] number of certificates sent",
		},
		{
			Name: numberOfSendingErrors,
			Help: "[AGGSENDER] number of sending errors",
		},
		{
			Name: numberOfSendingRetries,
			Help: "[AGGSENDER] number of sending retries",
		},
		{
			Name: numberOfSendingSuccesses,
			Help: "[AGGSENDER] number of sending successes",
		},
		{
			Name: certificateBuildTime,
			Help: "[AGGSENDER] certificate build time",
		},
		{
			Name: proverTime,
			Help: "[AGGSENDER] prover time",
		},
	}

	prometheus.RegisterGauges(gauges...)
}

// CertificateSent increments the gauge for the number of certificates sent
func CertificateSent() {
	prometheus.GaugeInc(numberOfCertificatesSent)
}

// SendingError increments the gauge for the number of sending errors
func SendingError() {
	prometheus.GaugeInc(numberOfSendingErrors)
}

// SendingRetry increments the gauge for the number of sending retries
func SendingRetry() {
	prometheus.GaugeInc(numberOfSendingRetries)
}

// SendingSuccess increments the gauge for the number of sending successes
func SendingSuccess() {
	prometheus.GaugeInc(numberOfSendingSuccesses)
}

// CertificateBuildTime sets the gauge for the certificate build time
func CertificateBuildTime(value float64) {
	prometheus.GaugeSet(certificateBuildTime, value)
}

// ProverTime sets the gauge for the prover time
func ProverTime(value float64) {
	prometheus.GaugeSet(proverTime, value)
}
