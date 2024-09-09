package projectLib

import projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"

func (h *repositoryHandler) CurrentBranch() (string, error) {
	config, err := h.Config()
	if err != nil {
		return "", err
	}

	code, err := h.Code()
	if err != nil {
		return "", err
	}

	configHead, err := config.Repo().Head()
	if err != nil {
		return "", err
	}
	configBranch := configHead.Name().Short()

	codeHead, err := code.Repo().Head()
	if err != nil {
		return "", err
	}
	codeBranch := codeHead.Name().Short()

	if configBranch != codeBranch {
		return "", projectI18n.ProjectBranchesNotEqual(configBranch, codeBranch)
	}

	return configBranch, nil
}
