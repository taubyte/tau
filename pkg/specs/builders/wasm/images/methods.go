package images

import (
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/taubyte/tau/pkg/specs/builders/wasm"
)

func RustImage(version string) string {
	return fmt.Sprintf(ImageNameFormat, TaubyteOrganization, RustRepository, version)
}

func GoImage(version string) string {
	return fmt.Sprintf(ImageNameFormat, TaubyteOrganization, GoRepository, version)
}

func AssemblyScriptImage(version string) string {
	return fmt.Sprintf(ImageNameFormat, TaubyteOrganization, AssemblyScriptRepository, version)
}

func Config(language wasm.SupportedLanguage) (lang LanguageConfig, err error) {
	lang, ok := languageConfigs[language]
	if !ok {
		err = fmt.Errorf("`%s` is not a supported language", language)
		return
	}

	return
}

func (l LanguageConfig) Image(version string) string {
	return l.imageMethod(version)
}

func (l LanguageConfig) TarBallPath(wd string) string {
	return path.Join(wd, TarBallBuildDir, ProductionDir, l.tarBallName)
}

func (l LanguageConfig) Language() wasm.SupportedLanguage {
	return l.language
}

func (l LanguageConfig) TestExamples() TestExampleLanguageConfig {
	return TestExampleLanguageConfig(l)
}

func (t TestExampleLanguageConfig) Language() wasm.SupportedLanguage {
	return t.language
}

func (t TestExampleLanguageConfig) Image() string {
	return t.imageMethod(TestExampleVersion)
}

func (t TestExampleLanguageConfig) TarBallPath(wd string) string {
	return path.Join(wd, TarBallBuildDir, TestExamplesDir, t.tarBallName)
}

func (e envVar) Set(value string) error {
	return os.Setenv(string(e), value)
}

func (e envVar) Get() (string, error) {
	value := os.Getenv(string(e))
	if len(value) < 1 {
		return "", fmt.Errorf("`%s` not set", e)
	}

	return value, nil
}

func (e envVar) Name() string {
	return string(e)
}

func (i imageEnvVar) LatestTag() (string, error) {
	image, err := i.Get()
	if err != nil {
		return "", err
	}

	split := strings.Split(image, ":")
	return strings.Join([]string{split[0], "latest"}, ":"), nil
}
