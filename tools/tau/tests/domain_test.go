package tests

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/constants"
	domainPrompts "github.com/taubyte/tau/tools/tau/prompts/domain"
)

// To view the test certificate info, current expires 2023
// openssl x509 -text -in testcert.crt | less

func specialWriteFilesInDir(hostName string, pathArgs ...string) func(dir string) {
	writeCertFiles := certWriteFilesInDir(hostName, pathArgs...)
	return func(dir string) {
		// run the original write
		writeCertFiles(dir)
		basicWriteFilesInDir("QmbJXVwFgwgMvgVHi5bsbz1R796qneHEhWArpz8fbNPsLs")(dir)
	}
}

func basicWriteFilesInDir(projectId string) func(dir string) {
	data := `id: ` + projectId + `
name: e2e
description: ""
notification:
    email: some@some.some`
	return func(dir string) {
		// Write project file
		projectFilePath, err := filepath.Abs(path.Join(dir, "test_project/config/config.yaml"))
		if err != nil {
			panic(fmt.Sprintf("Make path: %s failed with error: %s", path.Join("test_project/config/config.yaml"), err.Error()))
		}
		err = os.WriteFile(projectFilePath, []byte(data), 0640)
		if err != nil {
			panic(fmt.Sprintf("Write file: %s failed with error: %s", projectFilePath, err.Error()))
		}
	}
}

func basicNewDomain(name string, fqdn string) []string {
	return []string{
		"new", "-y", "domain",
		"--name", name,
		"--description", "some domain description",
		"--tags", "tag1, tag2,   tag3",
		"--fqdn", fqdn,
		"--cert-type", "auto",
		"--no-generated-fqdn",
	}
}

func TestDomainAll(t *testing.T) {
	t.Skip("authNodeUrl - use dream instead")
	runTests(t, createDomainMonkey(), true)
}

func createDomainMonkey() *testSpider {

	// Define shared variables
	// singularCamelCase := "Domain"
	camelCommand := "Domain"
	testName := "someDomain"
	profileName := "test"
	projectName := "test_project"
	command := "domain"
	certFile := "testcert.crt"
	keyFile := "key.key"
	network := "Test"

	// Create a basic resource of name
	basicNew := basicNewDomain

	// The config that will be written
	writeFilesInDir := basicGetConfigString(profileName, projectName)

	// Run before each test
	beforeEach := func(tt testMonkey) [][]string {
		tt.env[constants.CurrentProjectEnvVarName] = projectName
		tt.env[constants.CurrentSelectedNetworkName] = network
		return nil
	}

	// Define test monkeys that will run in parallel
	tests := []testMonkey{
		{
			mock:            true,
			name:            "New basic",
			args:            basicNew(testName, "domain-name0.com"),
			wantOut:         []string{camelCommand, testName, "Created"},
			writeFilesInDir: specialWriteFilesInDir("domain-name0.com"),
		},
		{
			mock:            true,
			name:            "Query new basic",
			args:            []string{"query", command, testName},
			writeFilesInDir: specialWriteFilesInDir("domain-name0.com"),
			preRun:          [][]string{basicNew(testName, "domain-name0.com")},
		},
		{
			mock: true,
			name: "New basic wrong domain name",
			args: []string{
				"new", command,
				"--name", testName,
				"--description", "some domain description",
				"--tags", "tag1, tag2,   tag3",
				"--fqdn", "domain-name0.com",
				"--cert-type", "inline",
				"--certificate", certFile,
				"--key", keyFile,
				"--no-generated-fqdn",
			},
			exitCode:        1,
			errOut:          []string{"failed to verify certificate; x509: certificate is valid for domain-name.com, not domain-name0.com"},
			writeFilesInDir: specialWriteFilesInDir("domain-name.com"),
		},
		{
			mock:            true,
			name:            "New basic with app",
			args:            basicNew(testName, "domain-name2.com"),
			wantOut:         []string{camelCommand, testName, "Created"},
			writeFilesInDir: specialWriteFilesInDir("domain-name2.com"),
			env: map[string]string{
				constants.CurrentApplicationEnvVarName: "someasdasdapp",
			},
		},
		{
			mock: true,
			name: "New basic no -y",
			args: []string{
				"new", command,
				"--name", testName,
				"--description", "some domain description",
				"--tags", "tag1, tag2,   tag3",
				"--fqdn", "domain-name3.com",
				"--cert-type", "auto",
				"--no-generated-fqdn",
			},
			exitCode:        1,
			errOut:          []string{"EOF"},
			writeFilesInDir: specialWriteFilesInDir("domain-name3.com"),
		},
		{
			mock: true,
			name: "Edit basic",
			args: []string{
				"edit", "-y", command,
				"--name", testName,
				"--description", "some newwenwenwenwen description",
				"--tags", "tag1, tag23,   tag3",
				"--fqdn", "domain-name4.com",
				"--cert-type", "auto",
			},
			wantOut:         []string{camelCommand, testName, "Edited"},
			writeFilesInDir: specialWriteFilesInDir("domain-name4.com"),
			preRun: [][]string{
				basicNew(testName, "domain-name4.com"),
			},
		},
		{
			mock: true,
			name: "Edit basic true to false",
			args: []string{
				"edit", "-y", command,
				"--name", testName,
				"--description", "some false description",
				"--tags", "tag1, tag23,   tag3",
				"--fqdn", "domain-name.com",
				"--type", "auto",
			},
			wantOut:         []string{camelCommand, testName, "Edited"},
			writeFilesInDir: specialWriteFilesInDir("domain-name.com"),
			preRun: [][]string{
				{
					"new", "-y", command,
					"--name", testName,
					"--description", "some newwenwenwenwen description",
					"--tags", "tag1, tag23,   tag3",
					"--fqdn", "domain-name.com",
					"--cert-type", "inline",
					"--certificate", certFile,
					"--key", keyFile,
					"--no-generated-fqdn",
				},
			},
		},
		{
			mock: true,
			name: "Edit basic false to true",
			args: []string{
				"edit", "-y", command,
				"--name", testName,
				"--description", "some false description",
				"--tags", "tag1, tag23,   tag3",
				"--fqdn", "domain-name6.com",
				"--type", "auto",
			},
			wantOut:         []string{camelCommand, testName, "Edited"},
			writeFilesInDir: specialWriteFilesInDir("domain-name6.com"),
			preRun: [][]string{
				{
					"new", "-y", command,
					"--name", testName,
					"--description", "some newwenwenwenwen description",
					"--tags", "tag1, tag23,   tag3",
					"--fqdn", "domain-name6.com",
					"--type", "auto",
					"--no-generated-fqdn",
				},
			},
		},
		{
			mock:            true,
			name:            "Edit basic query",
			args:            []string{"query", command, "--name", testName},
			wantOut:         []string{"newwenwenwenwen", "tag23"},
			writeFilesInDir: specialWriteFilesInDir("domain-name7.com.com"),
			preRun: [][]string{
				basicNew(testName, "domain-name7.com"),
				{
					"edit", "-y", command,
					"--name", testName,
					"--description", "some newwenwenwenwen description",
					"--tags", "tag1, tag23,   tag3",
					"--fqdn", "domain-name7.com",
					"--type", "auto",
				},
			},
		},
		{
			mock:            true,
			name:            "Delete basic",
			args:            []string{"delete", "-y", command, "--name", testName},
			wantOut:         []string{camelCommand, testName, "Deleted"},
			writeFilesInDir: specialWriteFilesInDir("domain-name8.com"),
			preRun: [][]string{
				basicNew(testName, "domain-name8.com"),
			},
		},
		{
			mock:            true,
			name:            "Delete basic Query",
			args:            []string{"query", command, "--name", testName},
			errOut:          []string{fmt.Sprintf(domainPrompts.NotFound, testName)},
			exitCode:        1,
			writeFilesInDir: specialWriteFilesInDir("domain-name9.com"),
			preRun: [][]string{
				basicNew(testName, "domain-name9.com"),
				{"delete", "-y", command, "--name", testName},
			},
		},
		{
			mock:            true,
			name:            "Delete basic with selected app",
			args:            []string{"delete", "-y", command, "--name", testName},
			writeFilesInDir: specialWriteFilesInDir("domain-name10.com"),
			wantOut:         []string{camelCommand, testName, "Deleted"},
			env: map[string]string{
				constants.CurrentApplicationEnvVarName: "someasdasdapp",
			},
			preRun: [][]string{
				basicNew(testName, "domain-name10.com"),
			},
		},
		{
			mock:            true,
			name:            "Query",
			args:            []string{"query", command, "--list"},
			wantOut:         []string{"someDomain1", "someapp2", "someapp3", "someapp4", "someapp5"},
			dontWantOut:     []string{"someapp13"},
			writeFilesInDir: specialWriteFilesInDir(""),
			preRun: [][]string{
				basicNew("someDomain1", "domain1-name0.com"),
				basicNew("someapp2", "domain2-name0.com"),
				basicNew("someapp3", "domain3-name0.com"),
				basicNew("someapp13", "domain4-name0.com"),
				{"delete", "-y", command, "--name", "someapp13"},
				basicNew("someapp4", "domain5-name0.com"),
				basicNew("someapp5", "domain6-name0.com"),
			},
		},
		{
			name:            "generated domain",
			mock:            true,
			writeFilesInDir: basicWriteFilesInDir("projectTestID"),
			args:            []string{"query", command, testName},
			wantOut:         []string{"cttestid0.g.tau.link"},
			children: []testMonkey{
				{
					name: "create domain",
					args: []string{
						"new", "-y", command,
						"--name", testName,
						"--description", "some domain description",
						"--tags", "tag1, tag2,   tag3",
						"--type", "auto",
						"--generated-fqdn",
					},
				},
			},
		},
		{
			name:            "generated domain with prefix",
			mock:            true,
			writeFilesInDir: basicWriteFilesInDir("projectTestID"),
			args:            []string{"query", command, testName},
			wantOut:         []string{"domain-prefix-cttestid0.g.tau.link"},
			children: []testMonkey{
				{
					name: "create domain",
					args: []string{
						"new", "-y", command,
						"--name", testName,
						"--description", "some domain description",
						"--tags", "tag1, tag2,   tag3",
						"--type", "auto",
						"--generated-fqdn",
						"--generated-fqdn-prefix", "domain-prefix",
					},
				},
			},
		},
	}
	return &testSpider{projectName, tests, beforeEach, writeFilesInDir, "domain"}
}
