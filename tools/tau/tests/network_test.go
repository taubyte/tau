package tests

import (
	"testing"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/constants"
)

func TestNetworkAll(t *testing.T) {
	runTests(t, createNetworkMonkey(), true)
}

func createNetworkMonkey() *testSpider {
	// Define shared variables
	profileName := "test"
	projectName := "test_project"
	fqdn := "sandbox.taubyte.com"

	// The config that will be written
	getConfigString := basicGetConfigString(profileName, projectName)

	// Run before each test
	beforeEach := func(tt testMonkey) [][]string {
		tt.env[constants.CurrentSelectedNetworkName] = ""
		return nil
	}

	// TODO: Add a dreamland test that starts and stop a dreamland instance
	tests := []testMonkey{
		{
			// FIXME: this test passes with debug being true for some reason
			name: "Select Remote network",
			args: []string{"select", "network", "--fqdn", fqdn},
			wantOut: []string{
				pterm.Success.Sprintf("Connected to %s", pterm.FgCyan.Sprintf(fqdn)),
			},
			evaluateSession: expectedSelectedCustomNetwork(common.RemoteNetwork, fqdn),
			debug:           true,
		},

		{
			name:            "Select login with network saved",
			args:            []string{"login", "--name", profileName},
			evaluateSession: expectedSelectedNetwork(common.RemoteNetwork),
		},
	}

	return &testSpider{projectName, tests, beforeEach, getConfigString, "network"}
}
