package common

import "github.com/taubyte/tau/pkg/specs/builders/wasm"

func GetLanguages() (languages []string) {
	for lang := range wasm.SupportedLanguages() {
		languages = append(languages, string(lang))
	}

	return
}
