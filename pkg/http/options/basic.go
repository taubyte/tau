package options

func Listen(uri string) Option {
	return func(s Configurable) error {
		return s.SetOption(OptionListen{On: uri})
	}
}

func Debug() Option {
	return func(s Configurable) error {
		return s.SetOption(OptionDebug{Debug: true})
	}
}
