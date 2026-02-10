package repositoryLib_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/taubyte/tau/tools/tau/common"
	commonTest "github.com/taubyte/tau/tools/tau/common/test"
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/constants"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	"github.com/taubyte/tau/tools/tau/session"
	"gotest.tools/v3/assert"
)

func TestInfo(t *testing.T) {
	token := commonTest.GitToken(t)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	sessionPath := filepath.Join(dir, "session")
	if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessionPath, 0755); err != nil {
		t.Fatal(err)
	}
	oldConfig := constants.TauConfigFileName
	constants.TauConfigFileName = configPath
	t.Cleanup(func() {
		constants.TauConfigFileName = oldConfig
		config.Clear()
		session.Clear()
	})

	session.Clear()
	config.Clear()
	assert.NilError(t, session.LoadSessionInDir(sessionPath))

	config.Profiles().Set("taubytetest", config.Profile{
		Provider:  "github",
		Token:     token,
		Default:   true,
		CloudType: common.RemoteCloud,
		Cloud:     "sandbox.taubyte.com",
	})
	assert.NilError(t, session.Set().ProfileName("taubytetest"))
	assert.NilError(t, session.Set().SelectedCloud("remote"))
	assert.NilError(t, session.Set().CustomCloudUrl("sandbox.taubyte.com"))

	info := &repositoryLib.Info{
		ID:   strconv.Itoa(commonTest.ConfigRepo.ID),
		Type: repositoryLib.WebsiteRepositoryType,
	}

	assert.NilError(t, info.GetNameFromID())

	expectedFullName := fmt.Sprintf("%s/%s", commonTest.GitUser, commonTest.ConfigRepo.Name)

	if info.FullName != expectedFullName {
		t.Errorf("Expected %s, got %s", expectedFullName, info.FullName)
		return
	}

	info.ID = ""
	assert.NilError(t, info.GetIDFromName())

	expectedID := commonTest.ConfigRepo.ID
	if info.ID != strconv.Itoa(expectedID) {
		t.Errorf("Expected %d, got %s", expectedID, info.ID)
		return
	}
}
