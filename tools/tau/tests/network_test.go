package tests

import (
	"testing"

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
			name:            "Select login with network saved",
			args:            []string{"login", "--name", profileName},
			evaluateSession: expectedSelectedNetwork(common.RemoteNetwork),
		},
	}

	return &testSpider{projectName, tests, beforeEach, getConfigString, "network"}
}
