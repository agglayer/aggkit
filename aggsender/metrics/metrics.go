package metrics

import (
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/prometheus"
	prometheusClient "github.com/prometheus/client_golang/prometheus"
)

const (
	prefix                      = "aggsender_"
	numberOfCertificatesSent    = prefix + "number_of_certificates_sent"
	numberOfCertificatesInError = prefix + "number_of_certificates_in_error"
	numberOfSendingRetries      = prefix + "number_of_sending_retries"
	numberOfCertificatesSettled = prefix + "number_of_sending_settled"
	certificateBuildTime        = prefix + "certificate_build_time"
	proverTime                  = prefix + "prover_time"
)

// Register the metrics for the aggsender package
func Register() {
	gauges := []prometheusClient.GaugeOpts{
		{
			Name: numberOfCertificatesSent,
			Help: "[AGGSENDER] number of certificates sent",
		},
		{
			Name: numberOfCertificatesInError,
			Help: "[AGGSENDER] number of certificates in error",
		},
		{
			Name: numberOfSendingRetries,
			Help: "[AGGSENDER] number of sending retries",
		},
		{
			Name: numberOfCertificatesSettled,
			Help: "[AGGSENDER] number of certificates settled",
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
	log.Info("Registered prometheus aggsender metrics")
}

// CertificateSent increments the gauge for the number of certificates sent
func CertificateSent() {
	prometheus.GaugeInc(numberOfCertificatesSent)
}

// InError increments the gauge for the number of certificates in error
func InError() {
	prometheus.GaugeInc(numberOfCertificatesInError)
}

// SendingRetry increments the gauge for the number of sending retries
func SendingRetry() {
	prometheus.GaugeInc(numberOfSendingRetries)
}

// Settled increments the gauge for the number of certificates settled
func Settled() {
	prometheus.GaugeInc(numberOfCertificatesSettled)
}

// CertificateBuildTime sets the gauge for the certificate build time
func CertificateBuildTime(value float64) {
	prometheus.GaugeSet(certificateBuildTime, value)
}

// ProverTime sets the gauge for the prover time
func ProverTime(value float64) {
	prometheus.GaugeSet(proverTime, value)
}
