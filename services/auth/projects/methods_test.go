package projects

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

func TestProjectObject_Serialize(t *testing.T) {
	project := &projectObject{
		id:       "test-project-123",
		name:     "Test Project",
		provider: "github",
		config:   "config-repo-123",
		code:     "code-repo-456",
	}

	data := project.Serialize()

	assert.Equal(t, data["id"], "test-project-123")
	assert.Equal(t, data["name"], "Test Project")
	assert.Equal(t, data["provider"], "github")
	assert.Equal(t, data["config"], "config-repo-123")
	assert.Equal(t, data["code"], "code-repo-456")
}

func TestProjectObject_Register(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	project := &projectObject{
		kv:       db,
		id:       "test-project-123",
		name:     "Test Project",
		provider: "github",
		config:   "config-repo-123",
		code:     "code-repo-456",
	}

	err = project.Register()
	assert.NilError(t, err)

	// Verify the data was stored
	name, err := db.Get(ctx, "/projects/test-project-123/name")
	assert.NilError(t, err)
	assert.Equal(t, string(name), "Test Project")

	provider, err := db.Get(ctx, "/projects/test-project-123/repositories/provider")
	assert.NilError(t, err)
	assert.Equal(t, string(provider), "github")

	config, err := db.Get(ctx, "/projects/test-project-123/repositories/config")
	assert.NilError(t, err)
	assert.Equal(t, string(config), "config-repo-123")

	code, err := db.Get(ctx, "/projects/test-project-123/repositories/code")
	assert.NilError(t, err)
	assert.Equal(t, string(code), "code-repo-456")
}

func TestProjectObject_Delete(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	project := &projectObject{
		kv:       db,
		id:       "test-project-123",
		name:     "Test Project",
		provider: "github",
		config:   "config-repo-123",
		code:     "code-repo-456",
	}

	// First register the project
	err = project.Register()
	assert.NilError(t, err)

	// Verify it exists
	assert.Assert(t, Exist(ctx, db, "test-project-123"))

	// Now delete it
	err = project.Delete()
	assert.NilError(t, err)

	// Verify it no longer exists
	assert.Assert(t, !Exist(ctx, db, "test-project-123"))
}

func TestProjectObject_Delete_NonExistentProject(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	project := &projectObject{
		kv:       db,
		id:       "non-existent-project",
		name:     "Test Project",
		provider: "github",
		config:   "config-repo-123",
		code:     "code-repo-456",
	}

	// Try to delete non-existent project
	err = project.Delete()
	assert.NilError(t, err) // Delete should succeed even if keys don't exist
}

func TestExist(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test with non-existent project
	exists := Exist(ctx, db, "non-existent-project")
	assert.Assert(t, !exists)

	// Create a project
	project := &projectObject{
		kv:       db,
		id:       "test-project-123",
		name:     "Test Project",
		provider: "github",
		config:   "config-repo-123",
		code:     "code-repo-456",
	}

	err = project.Register()
	assert.NilError(t, err)

	// Test with existing project
	exists = Exist(ctx, db, "test-project-123")
	assert.Assert(t, exists)
}

func TestFetch(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test fetching non-existent project
	_, err = Fetch(ctx, db, "non-existent-project")
	assert.Assert(t, err != nil)
	assert.Assert(t, err.Error() != "")

	// Create a project
	project := &projectObject{
		kv:       db,
		id:       "test-project-123",
		name:     "Test Project",
		provider: "github",
		config:   "config-repo-123",
		code:     "code-repo-456",
	}

	err = project.Register()
	assert.NilError(t, err)

	// Fetch the project
	fetchedProject, err := Fetch(ctx, db, "test-project-123")
	assert.NilError(t, err)
	assert.Assert(t, fetchedProject != nil)

	// Verify the fetched data
	assert.Equal(t, fetchedProject.Name(), "Test Project")
	assert.Equal(t, fetchedProject.Provider(), "github")
	assert.Equal(t, fetchedProject.Config(), "config-repo-123")
	assert.Equal(t, fetchedProject.Code(), "code-repo-456")
}

func TestFetch_PartialData(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	ctx := context.Background()
	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Store only partial project data
	err = db.Put(ctx, "/projects/partial-project/name", []byte("Partial Project"))
	assert.NilError(t, err)

	// Try to fetch project with missing data
	_, err = Fetch(ctx, db, "partial-project")
	assert.Assert(t, err != nil)
	assert.Assert(t, err.Error() != "")
}

func TestNew(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	data := Data{
		"id":       "test-project-123",
		"name":     "Test Project",
		"provider": "github",
		"config":   "config-repo-123",
		"code":     "code-repo-456",
	}

	project, err := New(db, data)
	assert.NilError(t, err)
	assert.Assert(t, project != nil)

	// Verify the project data
	assert.Equal(t, project.Name(), "Test Project")
	assert.Equal(t, project.Provider(), "github")
	assert.Equal(t, project.Config(), "config-repo-123")
	assert.Equal(t, project.Code(), "code-repo-456")
}

func TestNew_WithMissingData(t *testing.T) {
	mockKV := mock.New()
	defer mockKV.Close()

	db, err := mockKV.New(nil, "test", 5)
	assert.NilError(t, err)
	defer db.Close()

	// Test with missing data fields
	data := Data{
		"id":   "test-project-123",
		"name": "Test Project",
		// Missing provider, config, code
	}

	project, err := New(db, data)
	assert.NilError(t, err)
	assert.Assert(t, project != nil)

	// Verify default values for missing fields
	assert.Equal(t, project.Name(), "Test Project")
	assert.Equal(t, project.Provider(), "github") // Default value
	assert.Equal(t, project.Config(), "")
	assert.Equal(t, project.Code(), "")
}
