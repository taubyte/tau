package compile

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/schema/libraries"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	smartOpSpec "github.com/taubyte/tau/pkg/specs/smartops"
	"golang.org/x/exp/slices"
)

func checkLibExist(libIface libraries.Library, application string, err *error) {
	if libIface != nil {
		if len(libIface.Get().Id()) < 1 {
			*err = fmt.Errorf("library does not exist in application `%s`", application)
		}
	}
}

func getLibID(library, application string, project projectSchema.Project) (string, error) {
	if len(library) > 0 {
		// Check for the library locally
		libIface, err := project.Library(library, application)
		checkLibExist(libIface, application, &err)
		if err != nil {
			if len(application) > 0 {
				var _err error
				libIface, _err = project.Library(library, "")
				checkLibExist(libIface, "", &_err)
				if _err != nil {
					return "", fmt.Errorf("getting library failed with:\n%s\n%s", err, _err)
				}
			}

			return "", fmt.Errorf("getting library failed with: %s", err)
		}

		return libIface.Get().Id(), nil
	}

	return "", nil
}

func getDomIDs(domains []string, application string, project projectSchema.Project) ([]string, error) {
	domIDs := make([]string, 0)
	for _, dom := range domains {
		if len(dom) > 0 {

			// Check for the domain locally
			domIface, err1 := project.Domain(dom, application)
			if err1 != nil || domIface.Get().Id() == "" {
				var err2 error

				// Check for the domain globally
				domIface, err2 = project.Domain(dom, "")
				domID := domIface.Get().Id()
				if err2 != nil || domID == "" {
					if domID == "" {
						err1 = fmt.Errorf("domain not found")
					}
					return domIDs, fmt.Errorf("problems finding domain( %s ) %v  //  %v", dom, err1, err2)
				}
			}
			domIDs = append(domIDs, domIface.Get().Id())
		}
	}
	return domIDs, nil
}

func getSmartOpsFromTags(tags []string, application string, project projectSchema.Project, library string) ([]string, error) {
	opIDs := make([]string, 0)
	for _, tag := range tags {
		if len(tag) == 0 {
			continue
		}

		if strings.HasPrefix(tag, smartOpSpec.TagPrefix) {
			tag = strings.ReplaceAll(tag, smartOpSpec.TagPrefix, "")

			// Check for the smartOp locally
			opIface, err1 := project.SmartOps(tag, application)
			if err1 != nil || opIface.Get().Id() == "" {
				// Simply return if no application as that is equivalent to checking global
				if len(application) == 0 {
					if err1 == nil {
						err1 = fmt.Errorf("SmartOp not found")
					}
					return opIDs, fmt.Errorf("failed finding smartOp( %s ) %v", tag, err1)
				}

				// Check for the smartOp globally
				var err2 error

				opIface, err2 = project.SmartOps(tag, "")
				if err2 != nil || opIface.Get().Id() == "" {
					if opIface.Get().Id() == "" {
						err1 = fmt.Errorf("smartOp not found")
					}

					return opIDs, fmt.Errorf("failed finding smartOp( %s ) %v  //  %v", tag, err1, err2)
				}
			}
			opIDs = append(opIDs, opIface.Get().Id())
		}
	}

	// Check tags of dependent libraries for smartOps
	if len(library) > 0 {
		lib, err := project.Library(library, "")
		if err != nil {
			if len(application) != 0 {
				lib, err = project.Library(library, application)
			}
			if err != nil {
				return opIDs, err
			}
		}

		libSmartOps, err := getSmartOpsFromTags(lib.Get().Tags(), application, project, "")
		if err != nil {
			return opIDs, err
		}
		for _, smartOp := range libSmartOps {
			if !slices.Contains(opIDs, smartOp) {
				opIDs = append(opIDs, smartOp)
			}
		}
	}

	if len(application) > 0 {
		app, err := project.Application(application)
		if err != nil {
			return opIDs, err
		}

		// TODO this could be checked once for each application and cached,  the reason it's not is because
		// the applications are not always parsed before resources that require them
		appSmartOps, err := getSmartOpsFromTags(app.Get().Tags(), "", project, "")
		if err != nil {
			return opIDs, err
		}

		for _, smartOp := range appSmartOps {
			if !slices.Contains(opIDs, smartOp) {
				opIDs = append(opIDs, smartOp)
			}
		}
	}

	return opIDs, nil
}

func attachSmartOpsFromTags(returnMap map[string]interface{}, tags []string, application string, project projectSchema.Project, library string) error {
	smartOps, err := getSmartOpsFromTags(tags, application, project, library)
	if err != nil {
		return err
	}

	if len(smartOps) > 0 {
		returnMap["smartops"] = smartOps
	}

	return nil
}
