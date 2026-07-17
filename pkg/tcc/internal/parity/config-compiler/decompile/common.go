package decompile

import (
	"fmt"

	domLib "github.com/taubyte/tau/pkg/schema/domains"
	libraryLib "github.com/taubyte/tau/pkg/schema/libraries"
	projectLib "github.com/taubyte/tau/pkg/schema/project"
)

func (d *decompiler) cleanResources() (err error) {
	getter := d.project.Get()
	for _, app := range getter.Applications() {

		// app functions
		local, _ := getter.Functions(app)
		for _, name := range local {
			err = function_clean(d.project, name, app)
			if err != nil {
				return err
			}
		}

		// app websites
		local, _ = getter.Websites(app)
		for _, name := range local {
			err = website_clean(d.project, name, app)
			if err != nil {
				return err
			}
		}
	}
	// app functions
	_, global := getter.Functions("")
	for _, name := range global {
		err = function_clean(d.project, name, "")
		if err != nil {
			return err
		}
	}

	// // websites
	_, global = getter.Websites("")
	for _, name := range global {
		err = website_clean(d.project, name, "")
		if err != nil {
			return err
		}
	}

	return nil
}

func cleanLibs(project projectLib.Project, id string, app string) (string, error) {
	lib, err := libById(project, id, app)
	if err != nil || lib.Get().Id() == "" {
		if err != nil {
			return "", fmt.Errorf("library id:`%s` not found: %v", id, err)
		}
		return "", fmt.Errorf("library id:`%s` not found", id)
	}

	return lib.Get().Name(), nil
}

func libById(project projectLib.Project, _id string, app string) (_lib libraryLib.Library, err error) {
	local, global := project.Get().Libraries(app)
	for _, name := range local {
		_lib, err = project.Library(name, app)
		if err == nil && (_lib.Get().Id() == _id) {
			return
		}
	}

	for _, name := range global {
		_lib, err = project.Library(name, "")
		if err == nil && (_lib.Get().Id() == _id) {
			return
		}
	}
	return _lib, fmt.Errorf("library not found")
}

func cleanDoms(project projectLib.Project, old_domains []string, app string) ([]string, error) {
	new_domains := make([]string, 0)
	for _, _id := range old_domains {
		dom, err := domById(project, _id, app)
		if err != nil || dom.Get().Id() == "" {
			if err != nil {
				return new_domains, fmt.Errorf("domain id:`%s` not found: %v", _id, err)
			}
			return new_domains, fmt.Errorf("domain id:`%s` not found", _id)
		}
		new_domains = append(new_domains, dom.Get().Name())
	}
	return new_domains, nil
}

func domById(project projectLib.Project, _id string, app string) (_dom domLib.Domain, err error) {
	local, global := project.Get().Domains(app)
	for _, name := range local {
		_dom, err = project.Domain(name, app)
		if err == nil && (_dom.Get().Id() == _id) {
			return
		}
	}

	for _, name := range global {
		_dom, err = project.Domain(name, "")
		if err == nil && (_dom.Get().Id() == _id) {
			return
		}
	}
	return _dom, fmt.Errorf("domain not found")
}
