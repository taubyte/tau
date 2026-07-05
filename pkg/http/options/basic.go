package options

import "time"

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

// MaxBodyBytes caps incoming request bodies; <= 0 disables.
func MaxBodyBytes(limit int64) Option {
	return func(s Configurable) error {
		return s.SetOption(OptionMaxBodyBytes{Limit: limit})
	}
}

func ReadTimeout(d time.Duration) Option {
	return func(s Configurable) error {
		return s.SetOption(OptionReadTimeout{Duration: d})
	}
}

func WriteTimeout(d time.Duration) Option {
	return func(s Configurable) error {
		return s.SetOption(OptionWriteTimeout{Duration: d})
	}
}

func IdleTimeout(d time.Duration) Option {
	return func(s Configurable) error {
		return s.SetOption(OptionIdleTimeout{Duration: d})
	}
}

// ReadHeaderTimeout bounds slowloris without capping total body read time.
func ReadHeaderTimeout(d time.Duration) Option {
	return func(s Configurable) error {
		return s.SetOption(OptionReadHeaderTimeout{Duration: d})
	}
}
