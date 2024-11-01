package tests

import (
	"fmt"
	"testing"

	"github.com/taubyte/tau/pkg/schema/project"
	"github.com/taubyte/tau/tools/tau/constants"
	functionPrompts "github.com/taubyte/tau/tools/tau/prompts/function"
)

// Define a method to test your monkey
func TestFunctionAll(t *testing.T) {
	t.Skip("authNodeUrl - use dream instead")
	runTests(t, createFunctionMonkey(), true)
}

func createFunctionMonkey() *testSpider {

	// Define shared variables
	command := "function"
	profileName := "test"
	projectName := "test_project"
	testName := "test_function"

	testDomain := "test_domain_1"
	testDomainFqdn := "hal.computers.com"

	testService := "test_service_1"
	testServiceProtocol := "/test/v1"
	network := "Test"

	// Create a basic resource of name using a template
	basicNewTemplate := func(name, template string) []string {
		return []string{
			"new", "-y", command,
			"--name", name,
			"--description", "some function description",
			"--tags", "tag1, tag2,   tag3",
			"--timeout", "10s",
			"--memory", "10",
			"--memory-unit", "GB",
			"--type", "http",
			"--use-template",
			"--lang", "go",
			"--template", template,
			"--domains", "test_domain_1",
			"--method", "get",
			"--paths", "/",
			"--source", ".",
			"--call", "ping",
		}
	}

	basicNewNoTemplate := func(name string) []string {
		return []string{
			"new", "-y", command,
			"--name", name,
			"--description", "some function description",
			"--tags", "tag1, tag2,   tag3",
			"--timeout", "10s",
			"--memory", "10",
			"--memory-unit", "GB",
			"--type", "http",
			"--no-use-template",
			"--domains", "test_domain_1",
			"--method", "get",
			"--paths", "/",
			"--source", ".",
			"--call", "ping",
		}
	}

	testLibrary := "test_library"

	// The config that will be written
	getConfigString := basicGetConfigString(profileName, projectName)

	// Run before each test
	beforeEach := func(tt testMonkey) [][]string {
		tt.env[constants.CurrentProjectEnvVarName] = projectName
		tt.env[constants.CurrentSelectedNetworkName] = network
		return nil
	}

	// Define test monkeys that will run in parallel
	tests := []testMonkey{
		{
			name: "Query New basic with template",
			args: []string{"query", command, testName},
			wantOut: []string{
				"tag1", "tag2", "tag3", testName, testDomain, "GET",
				"Paths       │ /",
				"Source      │ .",
				"Call        │ ping",
				"test_domain_1",
			},
			mock: true,
			preRun: [][]string{
				basicNewDomain(testDomain, testDomainFqdn),
			},
			writeFilesInDir: specialWriteFilesInDir(testDomainFqdn),
			wantDir: []string{
				"test_project/code/functions/test_function/ping_pong.go",
			},
			children: []testMonkey{
				{
					args:    basicNewTemplate(testName, "ping_pong"),
					wantOut: []string{command, testName, "Created"},
				},
			},
		},
		{
			name: "Query New basic with template and application",
			mock: true,
			args: []string{"query", command, testName},
			wantOut: []string{
				"tag1", "tag2", "tag3", testName, testDomain, "GET",
				"Paths       │ /",
				"Source      │ .",
				"Call        │ ping",
				"test_domain_1",
			},
			preRun: [][]string{
				basicNewDomain(testDomain, testDomainFqdn),
			},
			writeFilesInDir: specialWriteFilesInDir(testDomainFqdn),
			wantDir: []string{
				"test_project/code/applications/test_app/functions/test_function/ping_pong.go",
			},
			env: map[string]string{
				constants.CurrentApplicationEnvVarName: "test_app",
			},
			children: []testMonkey{
				{
					args:    basicNewTemplate(testName, "ping_pong"),
					wantOut: []string{command, testName, "Created"},
				},
			},
		},
		{
			mock: true,
			name: "Query Edit basic with template",
			args: []string{"query", command, testName},
			wantOut: []string{
				"tag4", testName, "test_domain_2", "GET",
				"Paths       │ /, /test",
				"Source      │ libraries/test_library",
				"Call        │ test_library.ping",
				"10m",
				"50MB",
			},
			wantDir: []string{
				"test_project/code/functions/test_function/ping_pong.go",
			},
			children: []testMonkey{
				{
					args:    basicNewTemplate(testName, "ping_pong"),
					wantOut: []string{command, testName, "Created"},
				},
				{
					args: []string{
						"edit", "-y", command, "--name", testName,
						"--description", "some function description",
						"--tags", "tag4",
						"--timeout", "10m",
						"--memory", "50",
						"--memory-unit", "MB",
						"--type", "http",
						"--domains", "test_domain_2",
						"--method", "get",
						"--paths", "/,/test",
						"--source", "test_library",
						"--call", "test_library.ping",
					},
					wantOut: []string{command, testName, "Edited"},
				},
			},
			preRun: [][]string{
				basicNewLibrary(testLibrary),
				basicNewDomain(testDomain, testDomainFqdn),
				basicNewDomain("test_domain_2", testDomainFqdn),
			},
			writeFilesInDir: specialWriteFilesInDir(testDomainFqdn),
		},
		{
			name: "Query New basic no template",
			mock: true,
			args: []string{"query", command, testName},
			wantOut: []string{
				"tag1", "tag2", "tag3", testName, testDomain, "GET",
				"Paths       │ /",
				"Source      │ .",
				"Call        │ ping",
				"test_domain_1",
			},
			preRun: [][]string{
				basicNewDomain(testDomain, testDomainFqdn),
			},
			writeFilesInDir: specialWriteFilesInDir(testDomainFqdn),
			wantDir: []string{
				"test_project/code/functions/test_function/test_function.md",
			},
			children: []testMonkey{
				{
					args:    basicNewNoTemplate(testName),
					wantOut: []string{command, testName, "Created"},
				},
			},
		},
		{
			name: "Query Edit basic no template",
			mock: true,
			args: []string{"query", command, testName},
			wantOut: []string{
				"tag4", testName, "test_domain_2", "GET",
				"Paths       │ /, /test",
				"Source      │ libraries/test_library",
				"Call        │ test_library.ping",
				"10m",
				"50MB",
			},
			wantDir: []string{
				"test_project/code/functions/test_function/test_function.md",
			},
			preRun: [][]string{
				basicNewDomain(testDomain, testDomainFqdn),
				basicNewDomain("test_domain_2", testDomainFqdn),
				basicNewLibrary(testLibrary),
			},
			writeFilesInDir: specialWriteFilesInDir(testDomainFqdn),
			children: []testMonkey{
				{
					args:    basicNewNoTemplate(testName),
					wantOut: []string{command, testName, "Created"},
				},
				{
					args: []string{
						"edit", "-y", command, "--name", testName,
						"--description", "some function description",
						"--tags", "tag4",
						"--timeout", "10m",
						"--memory", "50",
						"--memory-unit", "MB",
						"--type", "http",
						"--domains", "test_domain_2",
						"--method", "get",
						"--paths", "/,/test",
						"--source", "test_library",
						"--call", "test_library.ping",
					},
					wantOut: []string{command, testName, "Edited"},
				},
			},
		},
		{
			name:     "Query delete",
			mock:     true,
			args:     []string{"query", command, testName},
			exitCode: 1,
			errOut:   []string{fmt.Sprintf(functionPrompts.NotFound, testName)},
			preRun: [][]string{
				basicNewDomain(testDomain, testDomainFqdn),
				basicNewTemplate(testName, "ping_pong"),
			},
			writeFilesInDir: specialWriteFilesInDir(testDomainFqdn),
			children: []testMonkey{
				{
					name:    "Delete basic",
					args:    []string{"delete", "-y", command, "--name", testName},
					wantOut: []string{command, testName, "Deleted"},
				},
			},
		},
		{
			name: "Query list",
			mock: true,
			args: []string{"query", command, "--list"},
			wantOut: []string{
				testName + "1",
				testName + "2",
				// testName+"3", deleted
				testName + "4",
				testName + "5",
			},
			dontWantOut: []string{
				testName + "3",
			},
			preRun: [][]string{
				basicNewDomain(testDomain, testDomainFqdn),
				basicNewTemplate(testName+"1", "ping_pong"),
				basicNewTemplate(testName+"2", "ping_pong"),
				basicNewTemplate(testName+"3", "ping_pong"),
				{"delete", "-y", command, "--name", testName + "3"},
				basicNewTemplate(testName+"4", "ping_pong"),
				basicNewTemplate(testName+"5", "ping_pong"),
			},
			writeFilesInDir: specialWriteFilesInDir(testDomainFqdn),
		},
		{
			name: "new p2p query",
			args: []string{"query", command, testName},
			wantOut: []string{
				"tag1", "tag2", "tag3", testName,
				"Command     │ doPing",
				"Source      │ .",
				"Call        │ ping",
				"Protocol    │ test_service_1",
			},
			wantDir: []string{
				"test_project/code/functions/test_function/test_function.md",
			},
			preRun: [][]string{
				basicNewService(testService, testServiceProtocol),
			},
			children: []testMonkey{
				{
					args: []string{
						"new", "-y", command, "--name", testName,
						"--description", "some function description",
						"--tags", "tag1,tag2,tag3",
						"--timeout", "10m",
						"--memory", "50",
						"--memory-unit", "MB",
						"--type", "p2p",
						"--no-use-template",
						"--command", "doPing",
						"--no-local",
						"--protocol", testServiceProtocol,
						"--source", "inline",
						"--call", "ping",
					},
					wantOut: []string{command, testName, "Created"},
				},
			},
		},
		{
			name: "new pubsub query",
			args: []string{"query", command, testName},
			wantOut: []string{
				"tag1", "tag2", "tag3", testName,
				"Channel     │ doPing",
				"Source      │ .",
				"Call        │ ping",
			},
			wantDir: []string{
				"test_project/code/functions/test_function/test_function.md",
			},
			children: []testMonkey{
				{
					args: []string{
						"new", "-y", command, "--name", testName,
						"--description", "some function description",
						"--tags", "tag1,tag2,tag3",
						"--timeout", "10m",
						"--memory", "50",
						"--memory-unit", "MB",
						"--type", "pubsub",
						"--no-use-template",
						"--channel", "doPing",
						"--no-local",
						"--paths", "/",
						"--source", "inline",
						"--call", "ping",
					},
					wantOut: []string{command, testName, "Created"},
				},
			},
		},
		{
			name: "edit http to p2p, query, confirm yaml",
			args: []string{"query", command, testName},
			wantOut: []string{
				"tag1", "tag2", "tag3", testName,
				"Command     │ doPing",
				"Source      │ .",
				"Call        │ ping",
			},
			mock: true,
			preRun: [][]string{
				basicNewDomain(testDomain, testDomainFqdn),
				basicNewService(testService, testServiceProtocol),
				basicNewTemplate(testName, "ping_pong"),
			},
			writeFilesInDir: specialWriteFilesInDir(testDomainFqdn),
			confirmProject: func(p project.Project) error {
				function, err := p.Function(testName, "")
				if err != nil {
					return nil
				}

				getter := function.Get()
				return ConfirmEmpty(getter.Method(), getter.Domains(), getter.Paths())
			},
			children: []testMonkey{
				{
					name: "edit http to p2p",
					args: []string{
						"edit", "-y", command, "--name", testName,
						"--description", "some function description",
						"--tags", "tag1,tag2,tag3",
						"--timeout", "10m",
						"--memory", "50",
						"--memory-unit", "MB",
						"--type", "p2p",
						"--command", "doPing",
						"--no-local",
						"--protocol", testServiceProtocol,
						"--source", "inline",
						"--call", "ping",
					},
					wantOut: []string{command, testName, "Edited"},
				},
			},
		},
		{
			name: "edit p2p to http, query, confirm yaml",
			args: []string{"query", command, testName},
			confirmProject: func(p project.Project) error {
				function, err := p.Function(testName, "")
				if err != nil {
					return nil
				}

				getter := function.Get()
				return ConfirmEmpty(getter.Command(), getter.Local(), getter.Protocol())
			},
			mock: true,
			preRun: [][]string{
				basicNewService(testService, testServiceProtocol),
				basicNewDomain("test_domain_2", testDomainFqdn),
				basicNewLibrary(testLibrary),
			},
			writeFilesInDir: specialWriteFilesInDir(testDomainFqdn),
			wantOut: []string{
				"test_function",
				"some function description",
				"tag4",
				"http",
				"test_domain_2",
				"/test",
				"GET",
				"libraries/test_library",
				"test_library.ping",
			},
			children: []testMonkey{
				{
					name: "new p2p",
					args: []string{
						"new", "-y", command, "--name", testName,
						"--description", "some function description",
						"--tags", "tag1,tag2,tag3",
						"--timeout", "10m",
						"--memory", "50",
						"--memory-unit", "MB",
						"--type", "p2p",
						"--no-use-template",
						"--command", "doPing",
						"--local",
						"--protocol", testServiceProtocol,
						"--source", "inline",
						"--call", "ping",
					},
					wantOut: []string{command, testName, "Created"},
				},
				{
					name: "edit p2p to http",
					args: []string{
						"edit", "-y", command, "--name", testName,
						"--description", "some function description",
						"--tags", "tag4",
						"--timeout", "10m",
						"--memory", "50",
						"--memory-unit", "MB",
						"--type", "http",
						"--domains", "test_domain_2",
						"--method", "get",
						"--paths", "/,/test",
						"--source", "test_library",
						"--call", "test_library.ping",
					},
					wantOut: []string{command, testName, "Edited"},
				},
			},
		},
		{
			name: "edit pubsub to p2p, query, confirm yaml",
			args: []string{"query", command, testName},
			confirmProject: func(p project.Project) error {
				function, err := p.Function(testName, "")
				if err != nil {
					return nil
				}

				getter := function.Get()
				return ConfirmEmpty(getter.Channel(), getter.Domains(), getter.Method(), getter.Paths())
			},
			preRun: [][]string{
				basicNewService(testService, testServiceProtocol),
			},
			writeFilesInDir: specialWriteFilesInDir(testDomainFqdn),
			wantOut: []string{
				"test_function",
				"some function description",
				"tag4",
				"p2p",
				"doPing",
				"true",
				"Protocol    │ test_service_1",
				"test_library",
				"test_library.ping",
			},
			children: []testMonkey{
				{
					name: "new pubsub",
					args: []string{
						"new", "-y", command, "--name", testName,
						"--description", "some function description",
						"--tags", "tag1,tag2,tag3",
						"--timeout", "10m",
						"--memory", "50",
						"--memory-unit", "MB",
						"--type", "pubsub",
						"--no-use-template",
						"--channel", "doPing",
						"--no-local",
						"--source", "inline",
						"--call", "ping",
					},
					wantOut: []string{command, testName, "Created"},
				},
				{
					name: "edit pubsub to p2p",
					args: []string{
						"edit", "-y", command, "--name", testName,
						"--description", "some function description",
						"--tags", "tag4",
						"--timeout", "10m",
						"--memory", "50",
						"--memory-unit", "MB",
						"--type", "p2p",
						"--command", "doPing",
						"--local",
						"--protocol", testServiceProtocol,
						"--source", "test_library",
						"--call", "test_library.ping",
					},
					wantOut: []string{command, testName, "Edited"},
				},
			},
		},
	}
	return &testSpider{projectName, tests, beforeEach, getConfigString, "function"}
}
