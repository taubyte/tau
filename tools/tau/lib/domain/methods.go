package domainLib

import (
	"github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func New(domain *structureSpec.Domain) (validator Validator, err error) {
	return set(domain, true)
}

func Set(domain *structureSpec.Domain) (validator Validator, err error) {
	return set(domain, false)
}

func Delete(name string) error {
	info, err := get(name)
	if err != nil {
		return err
	}

	return info.domain.Delete()
}

func List() ([]string, error) {
	_, _, domains, err := list()
	if err != nil {
		return nil, err
	}

	return domains, nil
}

func ListResources() ([]*structureSpec.Domain, error) {
	project, application, relative, err := list()
	if err != nil {
		return nil, err
	}

	domains := make([]*structureSpec.Domain, len(relative))
	for idx, name := range relative {
		domain, err := project.Domain(name, application)
		if err != nil {
			return nil, err
		}

		domains[idx], err = domain.Get().Struct()
		if err != nil {
			return nil, err
		}
	}

	return domains, nil
}

func ProjectDomainCount(project project.Project) (domainCount int) {
	_, global := project.Get().Domains("")
	domainCount += len(global)

	for _, app := range project.Get().Applications() {
		local, _ := project.Get().Domains(app)
		domainCount += len(local)
	}

	return
}
