package drive

import (
	"fmt"
	"os"
)

type Option func(Spore) error

func WithTauLatest() Option {
	return func(s Spore) error {
		r, err := getLatestAssetVersion()
		if err != nil {
			return fmt.Errorf("fetching latest release: %w", err)
		}

		s.(*sporedrive).tauBinary, err = downloadTau(r, "amd64")
		if err != nil {
			return fmt.Errorf("downloading latest (v%s) release: %w", r, err)
		}

		return nil
	}
}

func WithTauVersion(version string) Option {
	return func(s Spore) (err error) {
		s.(*sporedrive).tauBinary, err = downloadTau(version, "amd64")
		if err != nil {
			return fmt.Errorf("downloading %v release: %w", version, err)
		}

		return nil
	}
}

func WithTauUrl(url string) Option {
	return func(s Spore) (err error) {
		s.(*sporedrive).tauBinary, err = httpDownload(url)
		if err != nil {
			return fmt.Errorf("downloading tau: %w", err)
		}

		return nil
	}
}

func WithTauPath(path string) Option {
	return func(s Spore) (err error) {
		s.(*sporedrive).tauBinary, err = os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading tau from %s: %w", path, err)
		}

		return nil
	}
}
