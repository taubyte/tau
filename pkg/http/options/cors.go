package options

import (
	"crypto"
	"regexp"
)

func ACMEWithKey(directoryURL string, key crypto.Signer) Option {
	return func(s Configurable) error {
		return s.SetOption(OptionACME{DirectoryURL: directoryURL, Key: key})
	}
}

func ACME(directoryURL string) Option {
	return func(s Configurable) error {
		return s.SetOption(OptionACME{DirectoryURL: directoryURL})
	}
}

func AllowedMethods(methods []string) Option {
	return func(s Configurable) error {
		return s.SetOption(OptionAllowedMethods{Methods: methods})
	}
}

func AllowedOrigins(regex bool, origins []string) Option {
	return func(s Configurable) error {
		if !regex {
			return s.SetOption(OptionAllowedOrigins{
				Func: func(origin string) bool {
					for _, o := range origins {
						if origin == o {
							return true
						}
					}

					return false
				},
			})
		} else {
			_origins := make([]*regexp.Regexp, len(origins))
			for i, o := range origins {
				_origins[i] = regexp.MustCompile(o)
			}
			return s.SetOption(OptionAllowedOrigins{
				Func: func(origin string) bool {
					_origin := []byte(origin)
					for _, o := range _origins {
						if o.Match(_origin) {
							return true
						}
					}

					return false
				},
			})
		}
	}
}
