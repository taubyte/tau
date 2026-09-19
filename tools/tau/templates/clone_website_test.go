package templates_test

import (
	"os"
	"testing"

	"github.com/taubyte/tau/tools/tau/templates"
	"gotest.tools/v3/assert"
)

func TestCloneWebsite(t *testing.T) {
	// CloneTo makes its own destination, which is what its comment always
	// said and what this test used to do on its behalf.
	testFolder := "./assets/tb_website_someWebsite"
	defer os.RemoveAll("./assets")

	websites, err := templates.Get().Websites()
	if err != nil {
		t.Error(err)
		return
	}

	websiteInfo, ok := websites["Angular"]
	if !ok {
		t.Error("website not found")
		return
	}

	err = websiteInfo.CloneTo(testFolder)
	assert.NilError(t, err)

	dirs, err := os.ReadDir(testFolder)
	assert.NilError(t, err)

	if len(dirs) < 5 {
		t.Errorf("not enough files in folder %d expected at least 5", len(dirs))
		return
	}
}
