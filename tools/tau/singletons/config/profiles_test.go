package config_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/singletons/config"
)

func TestProfiles(t *testing.T) {
	_, deferment, err := initializeTest()
	if err != nil {
		t.Error(err)
		return
	}
	defer deferment()

	profiles := config.Profiles()

	testProfileName := "prof1"
	testProfile := config.Profile{
		Provider: "github",
		Token:    "123456",
		Default:  false,
	}

	err = profiles.Set(testProfileName, testProfile)
	if err != nil {
		t.Error(err)
		return
	}

	profile, err := profiles.Get(testProfileName)
	if err != nil {
		t.Error(err)
		return
	}

	if profile.Name() != testProfileName {
		t.Errorf("Expected profile name `%s`, got `%s`", testProfileName, profile.Name())
		return
	}

	if profile.Provider != testProfile.Provider {
		t.Errorf("Expected provider `%s`, got `%s`", testProfile.Provider, profile.Provider)
		return
	}

	if profile.Token != testProfile.Token {
		t.Errorf("Expected token `%s`, got `%s`", testProfile.Token, profile.Token)
		return
	}

	if profile.Default != testProfile.Default {
		t.Errorf("Expected default `%t`, got `%t`", testProfile.Default, profile.Default)
		return
	}

	expectedData := `profiles:
    prof1:
        provider: github
        token: "123456"
        default: false
        git_username: ""
        git_email: ""
        network: ""
        history: []
`

	configData, err := readConfig()
	if err != nil {
		t.Error(err)
		return
	}

	if configData != expectedData {
		t.Errorf("Expected %s, got %s", expectedData, configData)
		return
	}
}
