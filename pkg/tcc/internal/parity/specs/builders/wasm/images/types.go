package images

import "github.com/taubyte/tau/pkg/tcc/internal/parity/specs/builders/wasm"

type envVar string
type imageEnvVar struct {
	envVar
}

type TestExampleLanguageConfig LanguageConfig
type LanguageConfig struct {
	language    wasm.SupportedLanguage
	imageMethod func(string) string
	tarBallName string
}

type VersionedLang struct {
	Language LanguageConfig
	Version  string
}
