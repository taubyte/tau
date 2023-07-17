package tests

import (
	"fmt"

	"github.com/spf13/afero"
	projectSchema "github.com/taubyte/go-project-schema/project"
)

const rootDir = "/test_project/config/"

var fileContentsGlobal_Domain = `
id: testdomainid
description: 'test_domain'
tags: # optional
 - tagdomain1
 - tagdomain2
fqdn: taubyte.global.com.
certificate:
 type: inline
 key: testKey
 cert: testCert
`

var fileContentsLocal_Domain = `
id: testAppdomain
description: 'Test Appdomain'
tags: # optional
 - tagAppdomain1
 - tagAppdomain2
fqdn: taubyte.local.com.
certificate:
 type: inlineApp
 key: testKeyApp
 cert: testCertApp
`

var toWriteDomain = map[string]map[string]string{
	"test_domain_l": {
		"application": "someApp",
		"write":       fileContentsLocal_Domain,
	},
	"test_domain_g": {
		"application": "",
		"write":       fileContentsGlobal_Domain,
	},
}

func VirtualFSWithBuiltProject() (afero.Fs, error) {
	fs := afero.NewMemMapFs()
	err := fs.MkdirAll(rootDir, 0750)
	if err != nil {
		return nil, fmt.Errorf("make dir failed with: %v", err)
	}

	appName := "testapp"

	_, err = writeProject(fs)
	if err != nil {
		return nil, fmt.Errorf("write project failed with: %v", err)
	}

	fs, err = writeDomain(appName, fs)
	if err != nil {
		return nil, fmt.Errorf("write domains failed with: %v", err)
	}
	return fs, nil
}

func writeProject(fs afero.Fs) (projectSchema.Project, error) {
	project, err := projectSchema.Open(projectSchema.VirtualFS(fs, rootDir))
	if err != nil {
		return nil, err
	}

	err = project.Set(
		true,
		projectSchema.Id("Qmdwf4r8oY9TBjjexjjrdHgRCLJqDhSz2dgudTi7X4YtgX"),
		projectSchema.Name("test_project"),
		projectSchema.Description("Test Project"),
	)
	if err != nil {
		return nil, err
	}
	return project, nil
}

func writeDomain(application string, fs afero.Fs) (afero.Fs, error) {
	return writeFixture(fs, "domains", toWriteDomain)
}

func writeFixture(fs afero.Fs, folder string, toWrite map[string]map[string]string) (afero.Fs, error) {
	var root string
	var f afero.File
	var err error
	for name, data := range toWrite {
		application := data["application"]
		if len(application) > 0 {
			root = rootDir + "applications/" + application + "/" + folder
			f, err = fs.Create(root + "/" + name + ".yaml")
			if err != nil {
				return nil, err
			}
		} else {
			root = rootDir + folder
			f, err = fs.Create(root + "/" + name + ".yaml")
			if err != nil {
				return nil, err
			}
		}
		_, err = f.WriteString(data["write"])
		if err != nil {
			return nil, err
		}

		if f.Close() != nil {
			return nil, err
		}
	}

	return fs, nil
}
