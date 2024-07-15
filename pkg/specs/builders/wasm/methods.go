package wasm

import (
	"errors"
	"os"
	"path"

	"github.com/otiai10/copy"
	"github.com/taubyte/tau/pkg/git"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
)

func WasmOutput(outDir string) string {
	return path.Join(outDir, WasmFileName+WasmExt)
}

func WasmDeprecatedOutput(outDir string) string {
	return path.Join(outDir, DeprecatedWasmFile)
}

func (d Dir) WasmCompressed() string {
	return path.Join(d.String(), WasmFileName+WasmCompressedExt)
}

func (d Dir) Zip() string {
	return path.Join(d.String(), ZipFile)
}

func (s SupportedLanguage) Extension() string {
	return supportedLanguages[s]
}

func LangFromExt(ext string) *SupportedLanguage {
	for language := range supportedLanguages {
		if language.Extension() == ext {
			return &language
		}
	}

	return nil
}

func (s SupportedLanguage) CopyFunctionTemplateConfig(templateRepo *git.Repository, wd, destination string) error {
	if templateRepo == nil {
		return errors.New("template is nil")
	}

	if _, err := os.Stat(destination); err != nil {
		return err
	}

	templatePath := path.Join(wd, templateRepo.Dir(), "code", functionSpec.PathVariable.String(), string(s), "common")
	return copy.Copy(templatePath, destination)
}
