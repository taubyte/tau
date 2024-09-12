package dreamLib

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/pterm/pterm"
	libraryI18n "github.com/taubyte/tau/tools/tau/i18n/library"
	websiteI18n "github.com/taubyte/tau/tools/tau/i18n/website"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
)

func runJobs(jobs []internalJob) error {
	var (
		wg       sync.WaitGroup
		foundErr bool
	)

	wg.Add(len(jobs))
	for _, job := range jobs {
		go func(_job internalJob) {
			if err := _job(); err != nil {
				foundErr = true

				pterm.Error.Printfln("job failed: %s", err)
			}

			wg.Done()
		}(job)
	}
	wg.Wait()

	if foundErr {
		return errors.New("a job failed")
	}

	return nil
}

func (i *ProdProject) Import() error {
	// Attach the project to dreamland
	if err := i.Attach(); err != nil {
		return err
	}

	h := projectLib.Repository(i.Project.Get().Name())
	projectRepositories, err := h.Open()
	if err != nil {
		return err
	}

	branch, err := projectRepositories.CurrentBranch()
	if err != nil {
		return err
	}

	projectConfig, err := projectLib.SelectedProjectConfig()
	if err != nil {
		return err
	}

	getter := i.Project.Get()
	projectId := getter.Id()

	internalJobs := make([]internalJob, 0)
	addJob := func(appId, objId, path string) {
		internalJobs = append(internalJobs, func() error {
			job := &CompileForRepository{
				ProjectId:     projectId,
				ApplicationId: appId,
				ResourceId:    objId,
				Branch:        branch,
				Path:          path,
			}

			return job.Execute()
		})
	}

	addLibrary := func(name, appName, appID string) error {
		libObj, err := i.Project.Library(name, appName)
		if err != nil {
			return fmt.Errorf("reading library `%s/%s` failed with: %s", appName, name, err)
		}

		_, _, fullName := libObj.Get().Git()
		libraryPath := path.Join(projectConfig.LibraryLoc(), strings.Split(fullName, "/")[1])

		if _, err := os.Stat(libraryPath); err != nil {
			libraryI18n.Help().BeSureToCloneLibrary()
			return err
		}

		addJob(appID, libObj.Get().Id(), libraryPath)

		return nil
	}

	addWebsite := func(name, appName, appID string) error {
		webObj, err := i.Project.Website(name, appName)
		if err != nil {
			return fmt.Errorf("reading website `%s/%s` failed with: %s", appName, name, err)
		}

		_, _, fullName := webObj.Get().Git()
		websitePath := path.Join(projectConfig.WebsiteLoc(), strings.Split(fullName, "/")[1])

		if _, err := os.Stat(websitePath); err != nil {
			websiteI18n.Help().BeSureToCloneWebsite()
			return err
		}

		addJob(appID, webObj.Get().Id(), websitePath)

		return nil
	}

	// Build config
	b := &BuildLocalConfigCode{
		Config:      true,
		Code:        false,
		Branch:      branch,
		ProjectPath: projectConfig.Location,
		ProjectID:   projectId,
	}

	if err := b.Execute(); err != nil {
		return fmt.Errorf("building config failed with: %s", err)
	}

	// Attach building code to the internalJobs
	b.Config = false
	b.Code = true
	internalJobs = append(internalJobs, b.Execute)

	// Iterate over all global websites and libraries
	_, libraries := getter.Libraries("")
	for _, lib := range libraries {
		if err := addLibrary(lib, "", ""); err != nil {
			return err
		}
	}

	_, websites := getter.Websites("")
	for _, web := range websites {
		if err := addWebsite(web, "", ""); err != nil {
			return err
		}
	}

	// Iterate over websites and libraries within applications
	for _, app := range getter.Applications() {
		appObj, err := i.Project.Application(app)
		if err != nil {
			return fmt.Errorf("reading application `%s` failed with: %s", app, err)
		}
		appId := appObj.Get().Id()

		localLibraries, _ := getter.Libraries(app)
		for _, lib := range localLibraries {
			if err := addLibrary(lib, app, appId); err != nil {
				return err
			}
		}

		localWebsites, _ := getter.Websites(app)
		for _, web := range localWebsites {
			if err := addWebsite(web, app, appId); err != nil {
				return err
			}
		}
	}

	return runJobs(internalJobs)
}
