package args_test

import (
	"reflect"
	"testing"

	tauCLI "github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/cli/args"
	"github.com/urfave/cli/v2"
)

type testRunner struct {
	name         string
	testArgs     []string
	expectedArgs []string
	app          *cli.App
}

func TestArgs(t *testing.T) {
	realApp, err := tauCLI.New()
	if err != nil {
		t.Error(err)
		return
	}

	testCases := []testRunner{
		{
			name:         "global",
			testArgs:     []string{"program", "new", "function", "someFunc", "-g"},
			expectedArgs: []string{"program", "-g", "new", "function", "someFunc"},
			app: &cli.App{
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "global",
						Aliases: []string{"g"},
					},
				},
			},
		},
		{
			name:         "cmd",
			testArgs:     []string{"program", "function", "someFunc", "-g", "-c", "someCommand"},
			expectedArgs: []string{"program", "-g", "function", "-c", "someCommand", "someFunc"},
			app: &cli.App{
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "global",
						Aliases: []string{"g"},
					},
				},
				Commands: []*cli.Command{
					{
						Name: "function",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:    "command",
								Aliases: []string{"c"},
							},
						},
					},
				},
			},
		},
		{
			name:         "sub",
			testArgs:     []string{"program", "new", "function", "someFunc", "-g", "-c", "someCommand"},
			expectedArgs: []string{"program", "-g", "new", "function", "-c", "someCommand", "someFunc"},
			app: &cli.App{
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:    "global",
						Aliases: []string{"g"},
					},
				},
				Commands: []*cli.Command{
					{
						Name: "new",
						Subcommands: []*cli.Command{
							{
								Name: "function",
								Flags: []cli.Flag{
									&cli.StringFlag{
										Name:    "command",
										Aliases: []string{"c"},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name:         "sub",
			testArgs:     []string{"tau", "login", "someProfile2", "-p", "github", "-t", "token", "--new", "--color", "never"},
			expectedArgs: []string{"tau", "--color", "never", "login", "-p", "github", "-t", "token", "--new", "someProfile2"},
			app:          realApp,
		},
		{
			name:         "sub2",
			testArgs:     []string{"tau", "login", "--set-default", "profileName", "-t", "sometoken", "--env", "--color", "never", "--new"},
			expectedArgs: []string{"tau", "--env", "--color", "never", "login", "--set-default", "-t", "sometoken", "--new", "profileName"},
			app:          realApp,
		},
		{
			name:         "sub3",
			testArgs:     []string{"tau", "new", "-y", "application", "-n", "someApp", "-d", "some app desc", "-t", "some, other, tags"},
			expectedArgs: []string{"tau", "new", "application", "-y", "-n", "someApp", "-d", "some app desc", "-t", "some, other, tags"},
			app:          realApp,
		},
		{
			name:         "sub4",
			testArgs:     []string{"tau", "new", "application", "someApp", "-d", "some app desc"},
			expectedArgs: []string{"tau", "new", "application", "-d", "some app desc", "someApp"},
			app:          realApp,
		},
		{
			name:         "sub with alias",
			testArgs:     []string{"tau", "new", "-y", "app", "someApp", "-d", "some app desc"},
			expectedArgs: []string{"tau", "new", "app", "-y", "-d", "some app desc", "someApp"},
			app:          realApp,
		},
		{
			name:         "crazy command (example usage)",
			testArgs:     []string{"tau", "-y", "-d", "some app desc", "new", "someApp", "app"},
			expectedArgs: []string{"tau", "new", "app", "-y", "-d", "some app desc", "someApp"},
			app:          realApp,
		},
		{
			name:         "true attached on a bool flag parse",
			testArgs:     []string{"tau", "-env", "true", "-y", "true", "new", "someApp", "app"},
			expectedArgs: []string{"tau", "-env", "new", "app", "-y", "someApp"},
			app:          realApp,
		},
		{
			name:     "true attached on a bool flag parse",
			testArgs: []string{"tau", "-y", "true", "new", "someApp", "app", "-env", "false"},

			// Note: this will not work in practice for env, as it's not a required variable
			// It will work for variables that initialize --var and --no-var
			expectedArgs: []string{"tau", "-no-env", "new", "app", "-y", "someApp"},
			app:          realApp,
		},
		{
			name: "Using inverse bool flags",
			testArgs: []string{
				"tau", "new", "-y", "website",
				"-name", "someWebsite",
				"-description", "desc",
				"-tags", "tag1",
				"--no-generate-repository",
				"--paths", "/",
				"--repository-name", "tb_website_reactdemo",
				"--no-clone",
				"-b", "master",
				"--provider", "github",
				"--domains", "hal.computers.com",
			},
			expectedArgs: []string{
				"tau", "new", "website", "-y",
				"-name", "someWebsite",
				"-description", "desc",
				"-tags", "tag1",
				"--no-generate-repository",
				"--paths", "/",
				"--repository-name", "tb_website_reactdemo",
				"--no-clone",
				"-b", "master",
				"--provider", "github",
				"--domains", "hal.computers.com",
			},
			app: realApp,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			gotArgs := args.ParseArguments(testCase.app.Flags, testCase.app.Commands, testCase.testArgs...)

			if len(gotArgs) != len(testCase.expectedArgs) {
				t.Errorf("Expected %d args, got %d", len(testCase.expectedArgs), len(gotArgs))
			}

			if !reflect.DeepEqual(gotArgs, testCase.expectedArgs) {
				t.Errorf("\nExpected: %v\ngot     : %v", testCase.expectedArgs, gotArgs)
			}
		})
	}

}
