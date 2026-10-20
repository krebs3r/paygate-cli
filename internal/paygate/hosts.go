// Package paygate is a small client for the Nexi / Computop Paygate REST API v2.
package paygate

import (
	"fmt"
	"strings"
)

// Brand selects the host family. Nexi and Computop run the same platform and
// API, only the hostnames differ.
type Brand string

const (
	BrandNexi     Brand = "nexi"
	BrandComputop Brand = "computop"
)

// Env selects the dedicated test system or production.
type Env string

const (
	EnvTest Env = "test"
	EnvProd Env = "prod"
)

var origins = map[Brand]map[Env]string{
	BrandNexi: {
		EnvTest: "https://test.paygate.nexigroup.com",
		EnvProd: "https://paygate.nexigroup.com",
	},
	BrandComputop: {
		EnvTest: "https://test.computop-paygate.com",
		EnvProd: "https://www.computop-paygate.com",
	},
}

// Origin returns the scheme and host for a brand and environment.
func Origin(b Brand, e Env) (string, error) {
	envs, ok := origins[Brand(strings.ToLower(string(b)))]
	if !ok {
		return "", fmt.Errorf("unknown brand %q (use nexi or computop)", b)
	}
	o, ok := envs[Env(strings.ToLower(string(e)))]
	if !ok {
		return "", fmt.Errorf("unknown environment %q (use test or prod)", e)
	}
	return o, nil
}

// BrandLabel is the human-readable product name.
func BrandLabel(b Brand) string {
	switch b {
	case BrandComputop:
		return "Computop Paygate"
	default:
		return "Nexi Paygate"
	}
}
