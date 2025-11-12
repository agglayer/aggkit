package metrics

import (
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/prometheus"
	"github.com/ethereum/go-ethereum/common"
	prometheusClient "github.com/prometheus/client_golang/prometheus"
)

const (
	namespace                   = "aggsender"
	aggsenderValidatorLabel     = "aggsender_validator"
	numberOfCertificatesSent    = "number_of_certificates_sent"
	numberOfCertificatesInError = "number_of_certificates_in_error"
	numberOfSendingRetries      = "number_of_sending_retries"
	numberOfCertificatesSettled = "number_of_certificates_settled"
	certificateBuildTime        = "certificate_build_time"
	proverTime                  = "prover_time"
	numberOfProverErrors        = "number_of_prover_errors"
	validatorErrorNumber        = "validator_errors_total"
	validatorInvalidSignature   = "validator_invalid_signature_total"
	validateTime                = "validate_time"
	multiSigThresholdNotReached = "multisig_threshold_not_reached"
	certificateSettlementTime   = "certificate_settlement_time"
)

// Register the metrics for the aggsender package
func Register() {
	counters := []prometheusClient.CounterOpts{
		{
			Namespace: namespace,
			Name:      numberOfCertificatesSent,
			Help:      "Number of certificates sent",
		},
		{
			Namespace: namespace,
			Name:      numberOfCertificatesInError,
			Help:      "Number of certificates in error",
		},
		{
			Namespace: namespace,
			Name:      numberOfSendingRetries,
			Help:      "Number of sending retries",
		},
		{
			Namespace: namespace,
			Name:      numberOfCertificatesSettled,
			Help:      "Number of certificates settled",
		},
		{
			Namespace: namespace,
			Name:      numberOfProverErrors,
			Help:      "Number of prover errors",
		},
		{
			Namespace: namespace,
			Name:      multiSigThresholdNotReached,
			Help:      "Number of times multisig threshold was not reached",
		},
	}
	prometheus.RegisterCounters(counters...)

	counterVecs := []prometheus.CounterVecOpts{
		{
			CounterOpts: prometheusClient.CounterOpts{
				Namespace: namespace,
				Name:      validatorErrorNumber,
				Help:      "Total number of errors returned by a validator over time",
			},
			Labels: []string{aggsenderValidatorLabel},
		},
		{
			CounterOpts: prometheusClient.CounterOpts{
				Namespace: namespace,
				Name:      validatorInvalidSignature,
				Help:      "Number of times a validator returned an invalid signature",
			},
			Labels: []string{aggsenderValidatorLabel},
		},
	}
	prometheus.RegisterCounterVecs(counterVecs...)

	histograms := []prometheusClient.HistogramOpts{
		{
			Namespace: namespace,
			Name:      validateTime,
			Help:      "Time taken to validate a certificate",
			Buckets:   prometheusClient.DefBuckets,
		},
		{
			Namespace: namespace,
			Name:      proverTime,
			Help:      "Time taken by the prover",
			Buckets:   prometheusClient.DefBuckets,
		},
		{
			Namespace: namespace,
			Name:      certificateSettlementTime,
			Help:      "Time taken to settle a certificate",
			Buckets:   prometheusClient.DefBuckets,
		},
		{
			Namespace: namespace,
			Name:      certificateBuildTime,
			Help:      "Time taken to build a certificate",
			Buckets:   prometheusClient.DefBuckets,
		},
	}

	prometheus.RegisterHistograms(histograms...)

	log.Info("Registered prometheus aggsender metrics")
}

// CertificateSent increments the counter for the number of certificates sent
func CertificateSent() {
	prometheus.CounterInc(numberOfCertificatesSent)
}

// InError increments the counter for the number of certificates in error
func InError() {
	prometheus.CounterInc(numberOfCertificatesInError)
}

// SendingRetry increments the counter for the number of sending retries
func SendingRetry() {
	prometheus.CounterInc(numberOfSendingRetries)
}

// Settled increments the counter for the number of certificates settled
func Settled() {
	prometheus.CounterInc(numberOfCertificatesSettled)
}

// CertificateBuildTime provides a histogram for the certificate build time
func CertificateBuildTime(value float64) {
	prometheus.HistogramObserve(certificateBuildTime, value)
}

// ProverTime provides a histogram for the prover time
func ProverTime(value float64) {
	prometheus.HistogramObserve(proverTime, value)
}

// CertificateSettlementTime provides a histogram for the certificate settlement time
func CertificateSettlementTime(value float64) {
	prometheus.HistogramObserve(certificateSettlementTime, value)
}

// ProverError increments the counter for the number of prover errors
func ProverError() {
	prometheus.CounterInc(numberOfProverErrors)
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

// ValidateTime provides a histogram for the time taken to validate a certificate
func ValidateTime(value float64) {
	prometheus.HistogramObserve(validateTime, value)
}

// MultiSigThresholdNotReached increments the counter for the number of times
// the multisig threshold was not reached
func MultiSigThresholdNotReached() {
	prometheus.CounterInc(multiSigThresholdNotReached)
}
