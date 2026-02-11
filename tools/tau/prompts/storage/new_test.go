package storagePrompts_test

import (
	"testing"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/flags"
	storageFlags "github.com/taubyte/tau/tools/tau/flags/storage"
	storageLib "github.com/taubyte/tau/tools/tau/lib/storage"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	storagePrompts "github.com/taubyte/tau/tools/tau/prompts/storage"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestNew_AllFlagsSet_ObjectBucket_NonInteractive(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: flags.Combine(
			flags.Name,
			flags.Description,
			flags.Tags,
			flags.MatchRegex,
			flags.Match,
			storageFlags.Public,
			flags.Size,
			flags.SizeUnit,
			storageFlags.BucketType,
			storageFlags.Versioning,
		),
		ToSet: map[string]string{
			flags.Name.Name:              "storenew1",
			flags.Description.Name:       "A test storage",
			flags.Tags.Name:              "tag1",
			flags.Match.Name:             "/path",
			flags.Size.Name:              "10GB",
			storageFlags.BucketType.Name: storageLib.BucketObject,
		},
	}.Run("--name", "storenew1", "--description", "A test storage", "--tags", "tag1", "--match", "/path",
		"--regex", "--no-public", "--size", "10GB", "--bucket", storageLib.BucketObject, "--no-versioning")
	assert.NilError(t, err)

	s, err := storagePrompts.New(ctx)
	assert.NilError(t, err)
	assert.Assert(t, s != nil)
	assert.Equal(t, s.Name, "storenew1")
	assert.Equal(t, s.Description, "A test storage")
	assert.Equal(t, s.Match, "/path")
	assert.Equal(t, s.Type, storageLib.BucketObject)
}

func TestEdit_AllFlagsSet_ObjectBucket_NonInteractive(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: flags.Combine(
			flags.Description,
			flags.Tags,
			flags.Match,
			flags.MatchRegex,
			storageFlags.Public,
			flags.Size,
			flags.SizeUnit,
			storageFlags.BucketType,
			storageFlags.Versioning,
		),
		ToSet: map[string]string{
			flags.Description.Name:       "edited storage",
			flags.Tags.Name:              "t1",
			flags.Match.Name:             "/edit",
			flags.Size.Name:              "2GB",
			storageFlags.BucketType.Name: storageLib.BucketObject,
		},
	}.Run("--description", "edited storage", "--tags", "t1", "--match", "/edit", "--no-regex", "--no-public",
		"--size", "2GB", "--bucket", storageLib.BucketObject, "--no-versioning")
	assert.NilError(t, err)

	prev := &structureSpec.Storage{
		Name: "existing",
		Type: storageLib.BucketObject,
		Size: 1024,
	}
	err = storagePrompts.Edit(ctx, prev)
	assert.NilError(t, err)
	assert.Equal(t, prev.Description, "edited storage")
	assert.Equal(t, prev.Match, "/edit")
	assert.Equal(t, prev.Type, storageLib.BucketObject)
}
