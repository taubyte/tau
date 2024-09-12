package prompts

import (
	"fmt"
	"path"
	"strings"

	librarySpec "github.com/taubyte/tau/pkg/specs/library"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/env"
	"github.com/taubyte/tau/tools/tau/flags"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/urfave/cli/v2"
)

func GetOrSelectSource(ctx *cli.Context, prev ...string) (common.Source, error) {
	source := ctx.String(flags.Source.Name)
	sourceLC := strings.ToLower(source)

	var _default string
	if len(prev) > 0 {
		_default = prev[0]
	}

	sources := []string{string(common.SelectionInline)}
	project, err := projectLib.SelectedProjectInterface()
	if err != nil {
		return "", err
	}

	selectedApp, _ := env.GetSelectedApplication()
	local, global := project.Get().Libraries(selectedApp)
	for _, lib := range local {
		libLC := strings.ToLower(lib)
		if sourceLC == libLC || sourceLC == path.Join(librarySpec.PathVariable.String(), libLC) {
			return common.Source(path.Join(librarySpec.PathVariable.String(), lib)), nil
		}

		// attach app to previous selection
		appPlusLib := fmt.Sprintf("%s/%s/%s", selectedApp, librarySpec.PathVariable.String(), lib)
		if _default == lib {
			_default = appPlusLib
		}
		sources = append(sources, appPlusLib)
	}

	for _, lib := range global {
		libLC := strings.ToLower(lib)
		if sourceLC == libLC || sourceLC == path.Join(librarySpec.PathVariable.String(), libLC) {
			return common.Source(path.Join(librarySpec.PathVariable.String(), lib)), nil
		}
		sources = append(sources, path.Join(librarySpec.PathVariable.String(), lib))
	}

	if common.Source(sourceLC).Inline() {
		return common.SelectionInline, nil
	}

	source, err = SelectInterface(sources, SourcePrompt, _default)
	if err != nil {
		return "", err
	}

	return parseSource(source)
}

func parseSource(source string) (common.Source, error) {
	splitSource := strings.Split(source, "/")

	var (
		src common.Source
		err error
	)
	switch len(splitSource) {

	case 1: // source
		src = common.Source(splitSource[0])
	case 2: // ( app | library ) / source
		if splitSource[0] == librarySpec.PathVariable.String() {
			// libraries/source
			src = common.Source(strings.Join(splitSource[0:], "/"))
		} else {
			// app/source
			src = common.Source(splitSource[1])
		}
	case 3: // app/libraries/source
		src = common.Source(strings.Join(splitSource[1:], "/"))
	default:
		err = fmt.Errorf("invalid source, `%s`", source)
	}

	return src, err
}
