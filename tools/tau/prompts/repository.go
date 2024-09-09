package prompts

import (
	"fmt"
	"path"
	"strings"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/clients/http/auth/git/common"
	"github.com/taubyte/tau/tools/tau/flags"
	repositoryI18n "github.com/taubyte/tau/tools/tau/i18n/repository"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	"github.com/taubyte/tau/tools/tau/prompts/spinner"
	authClient "github.com/taubyte/tau/tools/tau/singletons/auth_client"
	"github.com/taubyte/tau/tools/tau/singletons/templates"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
)

func GetGenerateRepository(ctx *cli.Context, prev ...bool) bool {
	return GetOrAskForBool(ctx, flags.GenerateRepo.Name, GenerateRepoPrompt, prev...)
}

func GetOrRequireARepositoryName(ctx *cli.Context, prev ...string) string {
	return validateAndRequireString(ctx, validateRequiredStringHelper{
		field:     flags.RepositoryName.Name,
		prompt:    RepositoryNamePrompt,
		prev:      prev,
		validator: validate.VariableNameValidator,
	})
}

func SelectATemplate(ctx *cli.Context, templateMap map[string]templates.TemplateInfo, prevURL ...string) (url string, err error) {
	if ctx.IsSet(flags.Template.Name) {
		selectedTemplate := strings.ToLower(ctx.String(flags.Template.Name))

		LTemplateNames := []string{}
		for name, template := range templateMap {
			if selectedTemplate == template.URL {
				return template.URL, nil
			}

			LName := strings.ToLower(name)
			LTemplateNames = append(LTemplateNames, LName)

			if selectedTemplate == LName {
				return template.URL, nil
			}
		}

		pterm.Warning.Println(repositoryI18n.UnknownTemplate(selectedTemplate, LTemplateNames))
	}

	options, selector, _default := buildTemplateOptions(templateMap, prevURL...)

	selected, err := SelectInterface(options, SelectTemplatePrompt, _default)
	if err != nil {
		return
	}

	selection, ok := selector(selected)
	if !ok {
		return "", fmt.Errorf("selecting `%s` failed with not OK", selected)
	}

	return selection.URL, nil
}

type templateOptionSelector = func(selection string) (info templates.TemplateInfo, ok bool)

func buildTemplateOptions(templateMap map[string]templates.TemplateInfo, prev ...string) (options []string, selector templateOptionSelector, _default string) {
	options = make([]string, len(templateMap))
	optionSelect := make(map[string]templates.TemplateInfo, len(templateMap))

	var previous string
	if len(prev) > 0 {
		previous = prev[0]
	}

	var idx int
	for name, template := range templateMap {
		var option string
		if template.HideURL {
			if len(template.Description) > 0 {
				option = fmt.Sprintf("( %s ): %s", name, template.Description)
			} else {
				option = fmt.Sprintf("( %s )", name)
			}
		} else {
			if len(template.Description) > 0 {
				option = fmt.Sprintf("( %s ): %s\n%s", name, template.URL, template.Description)
			} else {
				option = fmt.Sprintf("( %s ): %s", name, template.URL)
			}
		}

		if template.URL == previous {
			_default = option
		}

		options[idx] = option
		optionSelect[option] = template
		idx++
	}

	selector = func(selection string) (info templates.TemplateInfo, ok bool) {
		info, ok = optionSelect[selection]
		return
	}

	return
}

// repository-id || repository-name/full-name
func SelectARepository(ctx *cli.Context, prev *repositoryLib.Info) (*repositoryLib.Info, error) {
	repoIdSet := ctx.IsSet(flags.RepositoryId.Name)
	repoNameSet := ctx.IsSet(flags.RepositoryName.Name)
	if repoIdSet && repoNameSet {

		info := &repositoryLib.Info{
			ID:       ctx.String(flags.RepositoryId.Name),
			FullName: ctx.String(flags.RepositoryName.Name),
			Type:     prev.Type,
		}

		if strings.Contains(info.FullName, "/") {
			return info, nil
		}

		profile, err := loginLib.GetSelectedProfile()
		if err != nil {
			return nil, err
		}

		info.FullName = path.Join(profile.GitUsername, info.FullName)

		return info, nil
	}

	info := &repositoryLib.Info{
		Type: prev.Type,
	}

	var err error
	if repoIdSet {
		info.ID = ctx.String(flags.RepositoryId.Name)

		err = info.GetNameFromID()
		if err != nil {
			return nil, err
		}

		return info, err
	} else if repoNameSet {
		info.FullName = ctx.String(flags.RepositoryName.Name)
		if !strings.Contains(info.FullName, "/") {
			profile, err := loginLib.GetSelectedProfile()
			if err != nil {
				return nil, err
			}

			info.FullName = path.Join(profile.GitUsername, info.FullName)
		}

		err = info.GetIDFromName()
		if err != nil {
			return nil, err
		}

		return info, err

	} else if len(prev.FullName) > 0 {
		// Skipping Select for edit
		return prev, nil
	}

	repo, err := SelectARepositoryFromGithub()
	if err != nil {
		return nil, err
	}

	info.ID = repo.Get().ID()
	info.FullName = repo.Get().FullName()

	return info, nil
}

func SelectARepositoryFromGithub() (common.Repository, error) {
	client, err := authClient.Load()
	if err != nil {
		return nil, err
	}

	stopGlobe := spinner.Globe()
	repos, err := client.ListRepositories()
	stopGlobe()
	if err != nil {
		return nil, err
	}

	optionMap := make(map[string]common.Repository, len(repos))
	options := make([]string, len(repos))
	for idx, repo := range repos {
		fullName := repo.Get().FullName()

		optionMap[fullName] = repo
		options[idx] = fullName
	}

	selected, err := SelectInterface(options, RepositorySelectPrompt, "")
	if err != nil {
		return nil, err
	}

	repo, ok := optionMap[selected]
	if !ok {
		return nil, fmt.Errorf("selecting `%s` failed with not OK", selected)
	}

	return repo, nil
}
