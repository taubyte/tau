package databasePrompts_test

import (
	"testing"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/flags"
	databaseFlags "github.com/taubyte/tau/tools/tau/flags/database"
	"github.com/taubyte/tau/tools/tau/prompts"
	databasePrompts "github.com/taubyte/tau/tools/tau/prompts/database"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestNew_AllFlagsSet_NonInteractive(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: flags.Combine(
			flags.Name,
			flags.Description,
			flags.Tags,
			flags.Match,
			flags.MatchRegex,
			flags.Local,
			databaseFlags.Encryption,
			databaseFlags.EncryptionKey,
			databaseFlags.Min,
			databaseFlags.Max,
			flags.Size,
			flags.SizeUnit,
		),
		ToSet: map[string]string{
			flags.Name.Name:        "dbnew1",
			flags.Description.Name: "A test db",
			flags.Tags.Name:        "tag1",
			flags.Match.Name:       "/path",
			databaseFlags.Min.Name: "1",
			databaseFlags.Max.Name: "10",
			flags.Size.Name:        "10GB",
		},
	}.Run("--name", "dbnew1", "--description", "A test db", "--tags", "tag1", "--match", "/path",
		"--regex", "--local", "--no-encryption", "--min", "1", "--max", "10", "--size", "10GB")
	assert.NilError(t, err)

	db, err := databasePrompts.New(ctx)
	assert.NilError(t, err)
	assert.Assert(t, db != nil)
	assert.Equal(t, db.Name, "dbnew1")
	assert.Equal(t, db.Description, "A test db")
	assert.Equal(t, db.Match, "/path")
	assert.Equal(t, db.Min, 1)
	assert.Equal(t, db.Max, 10)
}

func TestEdit_AllFlagsSet_NonInteractive(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: flags.Combine(
			flags.Description,
			flags.Tags,
			flags.Match,
			flags.MatchRegex,
			flags.Local,
			databaseFlags.Encryption,
			databaseFlags.Min,
			databaseFlags.Max,
			flags.Size,
			flags.SizeUnit,
		),
		ToSet: map[string]string{
			flags.Description.Name: "edited desc",
			flags.Tags.Name:        "t1",
			flags.Match.Name:       "/edit",
			databaseFlags.Min.Name: "2",
			databaseFlags.Max.Name: "20",
			flags.Size.Name:        "5GB",
		},
	}.Run("--description", "edited desc", "--tags", "t1", "--match", "/edit", "--no-regex", "--no-local",
		"--no-encryption", "--min", "2", "--max", "20", "--size", "5GB")
	assert.NilError(t, err)

	prev := &structureSpec.Database{
		Name: "existing",
		Min:  1,
		Max:  10,
		Size: 1024,
	}
	err = databasePrompts.Edit(ctx, prev)
	assert.NilError(t, err)
	assert.Equal(t, prev.Description, "edited desc")
	assert.Equal(t, prev.Match, "/edit")
	assert.Equal(t, prev.Min, 2)
	assert.Equal(t, prev.Max, 20)
}
