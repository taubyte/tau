package options

import (
	"crypto"
	"crypto/x509"
	"time"
)

type Configurable interface {
	SetOption(any) error
}

type Option func(s Configurable) error

type OptionACME struct {
	Key          crypto.Signer
	DirectoryURL string
}

// OptionACMECARoots / OptionACMECASkipVerify customise the TLS verification
// the autocert client uses when talking to the ACME directory itself.
// Useful for private / staging CAs that aren't in the system trust store.
type OptionACMECARoots struct{ Roots *x509.CertPool }
type OptionACMECASkipVerify struct{ Skip bool }

type OptionAllowedMethods struct {
	Methods []string
}

type OptionAllowedOrigins struct {
	Func func(origin string) bool
}

type OptionListen struct {
	On string
}

type OptionLoadCertificate struct {
	CertificateFilename string
	KeyFilename         string
}

type OptionTryLoadCertificate OptionLoadCertificate

type OptionSelfSignedCertificate struct{}

type OptionDebug struct {
	Debug bool
}

// OptionMaxBodyBytes caps request bodies; zero = no limit.
type OptionMaxBodyBytes struct {
	Limit int64
}

// Zero on these falls back to Go's net/http defaults (no timeout).
type OptionReadTimeout struct{ Duration time.Duration }
type OptionWriteTimeout struct{ Duration time.Duration }
type OptionIdleTimeout struct{ Duration time.Duration }
type OptionReadHeaderTimeout struct{ Duration time.Duration }
