package common_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/common"
	"gotest.tools/v3/assert"
)

func TestSource_Inline(t *testing.T) {
	assert.Equal(t, common.Source(".").Inline(), true)
	assert.Equal(t, common.Source("inline").Inline(), true)
	assert.Equal(t, common.Source(common.SourceLibraryPrefix+"foo").Inline(), false)
	assert.Equal(t, common.Source("").Inline(), false)
}

func TestSource_String(t *testing.T) {
	assert.Equal(t, common.Source(".").String(), ".")
	assert.Equal(t, common.Source("library/name").String(), "library/name")
	assert.Equal(t, common.Source("").String(), "")
}

func TestCloudConstants(t *testing.T) {
	assert.Equal(t, common.TestCloud, "test")
	assert.Equal(t, common.RemoteCloud, "remote")
	assert.Equal(t, common.DreamCloud, "dream")
}

func TestDefaultURLs(t *testing.T) {
	assert.Assert(t, len(common.DefaultAuthUrl) > 0)
	assert.Assert(t, len(common.DefaultPatrickUrl) > 0)
	assert.Assert(t, len(common.DefaultSeerUrl) > 0)
}

func TestVersion(t *testing.T) {
	assert.Assert(t, len(common.Version) > 0)
	line := common.VersionLine()
	assert.Assert(t, len(line) > 0)
	assert.Assert(t, len(common.FunctionTypes) > 0)
	assert.Assert(t, len(common.BucketTypes) > 0)
}
