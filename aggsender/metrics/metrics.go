package metrics

import (
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/prometheus"
	"github.com/ethereum/go-ethereum/common"
	prometheusClient "github.com/prometheus/client_golang/prometheus"
)

const (
	prefix                      = "aggsender_"
	aggsenderValidator          = "aggsender_validator"
	numberOfCertificatesSent    = prefix + "number_of_certificates_sent"
	numberOfCertificatesInError = prefix + "number_of_certificates_in_error"
	numberOfSendingRetries      = prefix + "number_of_sending_retries"
	numberOfCertificatesSettled = prefix + "number_of_sending_settled"
	certificateBuildTime        = prefix + "certificate_build_time"
	proverTime                  = prefix + "prover_time"
	numberOfProverErrors        = prefix + "number_of_prover_errors"
	validatorErrorNumber        = prefix + "validator_errors_total"
	validatorInvalidSignature   = prefix + "validator_invalid_signature_total"
	validateTime                = prefix + "validate_time"
	multiSigThresholdNotReached = prefix + "multisig_threshold_not_reached"
	certificateSettlementTime   = prefix + "certificate_settlement_time"
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
			Name: numberOfProverErrors,
			Help: "[AGGSENDER] number of prover errors",
		},
		{
			Name: multiSigThresholdNotReached,
			Help: "[AGGSENDER] number of times multisig threshold was not reached",
		},
	}
	prometheus.RegisterGauges(gauges...)

	counterVecs := []prometheus.CounterVecOpts{
		{
			CounterOpts: prometheusClient.CounterOpts{
				Name: validatorErrorNumber,
				Help: "[AGGSENDER] total number of errors returned by a validator over time",
			},
			Labels: []string{aggsenderValidator},
		},
		{
			CounterOpts: prometheusClient.CounterOpts{
				Name: validatorInvalidSignature,
				Help: "[AGGSENDER] number of times a validator returned an invalid signature",
			},
			Labels: []string{aggsenderValidator},
		},
	}
	prometheus.RegisterCounterVecs(counterVecs...)

	histograms := []prometheusClient.HistogramOpts{
		{
			Name:    validateTime,
			Help:    "[AGGSENDER] time taken to validate a certificate",
			Buckets: prometheusClient.DefBuckets,
		},
		{
			Name:    proverTime,
			Help:    "[AGGSENDER] time taken by the prover",
			Buckets: prometheusClient.DefBuckets,
		},
		{
			Name:    certificateSettlementTime,
			Help:    "[AGGSENDER] time taken to settle a certificate",
			Buckets: prometheusClient.DefBuckets,
		},
		{
			Name:    certificateBuildTime,
			Help:    "[AGGSENDER] time taken to build a certificate",
			Buckets: prometheusClient.DefBuckets,
		},
	}

	prometheus.RegisterHistograms(histograms...)

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
	prometheus.HistogramObserve(certificateBuildTime, value)
}

// ProverTime sets the gauge for the prover time
func ProverTime(value float64) {
	prometheus.HistogramObserve(proverTime, value)
}

// CertificateSettlementTime sets the gauge for the certificate settlement time
func CertificateSettlementTime(value float64) {
	prometheus.HistogramObserve(certificateSettlementTime, value)
}

// ProverError increments the gauge for the number of prover errors
func ProverError() {
	prometheus.GaugeInc(numberOfProverErrors)
}

// ValidatorError increments the counter for the number of validator errors, labeled by validator address
func ValidatorError(validator common.Address) {
	prometheus.CounterVecInc(validatorErrorNumber, validator.String())
}

// ValidatorInvalidSignature increments the counter for the number of
// invalid signatures from a validator, labeled by validator address
func ValidatorInvalidSignature(validator common.Address) {
	prometheus.CounterVecInc(validatorInvalidSignature, validator.String())
}

// ValidateTime sets the gauge for the time taken to validate a certificate
func ValidateTime(value float64) {
	prometheus.HistogramObserve(validateTime, value)
}

// MultiSigThresholdNotReached increments the gauge for the number of times
// the multisig threshold was not reached
func MultiSigThresholdNotReached() {
	prometheus.GaugeInc(multiSigThresholdNotReached)
}
