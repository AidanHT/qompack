// Package config holds the service's deployment settings.
package config

// Region returns the deployment region code.
func Region() string {
	return "QX-7731-EU"
}

// MaxRetries is the attempt limit of the payment-gateway retry policy.
func MaxRetries() int {
	return 7
}
