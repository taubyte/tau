package prompts

import (
	"strings"

	"github.com/taubyte/tau/pkg/cli/common"
	"github.com/taubyte/tau/pkg/specs/builders/wasm"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
	"golang.org/x/exp/slices"
)

func getLanguage(ctx *cli.Context) string {
	lang := ctx.String(flags.Language.Name)
	if len(lang) == 0 {
		return lang
	}

	langLowerCase := strings.ToLower(lang)
	for _lang, aliases := range wasm.LanguageAliases() {
		if slices.Contains(aliases, langLowerCase) {
			return string(_lang)
		}
	}

	return langLowerCase
}

func GetOrSelectLanguage(ctx *cli.Context, prev ...string) (string, error) {
	language := getLanguage(ctx)
	var languages []string
	for _, lang := range common.GetLanguages() {
		if language == strings.ToLower(lang) {
			return lang, nil
		}
		languages = append(languages, lang)
	}

	var _default string
	if len(prev) > 0 {
		_default = prev[0]
	}

	var err error
	language, err = SelectInterface(languages, CodeLanguagePrompt, _default)
	if err != nil {
		return "", err
	}

	return language, nil
}
