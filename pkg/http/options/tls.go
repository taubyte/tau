package options

func SelfSignedCertificate() Option {
	return func(s Configurable) error {
		return s.SetOption(OptionSelfSignedCertificate{})
	}
}

func LoadCertificate(certificate string, key string) Option {
	return func(s Configurable) error {
		return s.SetOption(OptionLoadCertificate{
			CertificateFilename: certificate,
			KeyFilename:         key,
		})
	}
}

func TryLoadCertificate(certificate string, key string) Option {
	return func(s Configurable) error {
		return s.SetOption(OptionTryLoadCertificate{
			CertificateFilename: certificate,
			KeyFilename:         key,
		})
	}
}
