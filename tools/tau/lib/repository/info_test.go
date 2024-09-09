package repositoryLib_test

import (
	"fmt"
	"strconv"
	"testing"

	commonTest "github.com/taubyte/tau/tools/tau/common/test"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	"github.com/taubyte/tau/tools/tau/singletons/session"
	"gotest.tools/v3/assert"
)

func TestInfo(t *testing.T) {
	t.Skip("Needs re-factor")
	assert.NilError(t, session.Set().ProfileName("taubytetest"))
	assert.NilError(t, session.Set().SelectedNetwork("Remote"))
	assert.NilError(t, session.Set().CustomNetworkUrl("sandbox.taubyte.com"))

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
