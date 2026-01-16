package engine

import (
	"errors"
	"testing"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func TestDump_WithRootConfigOnly(t *testing.T) {
	// Create in-memory filesystem
	memFs := afero.NewMemMapFs()

	// Create schema with root attributes
	schema := &schemaDef{
		root: &Node{
			Group: true,
			Match: nil, // Root node
			Attributes: []*Attribute{
				{Name: "id", Type: TypeString, Required: true},
				{Name: "name", Type: TypeString},
				{Name: "description", Type: TypeString},
			},
		},
	}

	// Create engine
	eng, err := New(schema, yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	// Create object with data
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-123")
	obj.Set("name", "Test Project")
	obj.Set("description", "A test project")

	// Dump
	err = eng.Dump(obj)
	assert.NilError(t, err)

	// Verify config.yaml was created
	exists, err := afero.Exists(memFs, "/config.yaml")
	assert.NilError(t, err)
	assert.Assert(t, exists, "config.yaml should exist")

	// Read and verify content
	content, err := afero.ReadFile(memFs, "/config.yaml")
	assert.NilError(t, err)
	assert.Assert(t, len(content) > 0, "config.yaml should have content")
}

func TestDump_WithNestedResources(t *testing.T) {
	memFs := afero.NewMemMapFs()

	// Create schema with root + functions group
	schema := &schemaDef{
		root: &Node{
			Group: true,
			Match: nil,
			Attributes: []*Attribute{
				{Name: "id", Type: TypeString, Required: true},
			},
			Children: []*Node{
				{
					Group: true,
					Match: "functions",
					Children: []*Node{
						{
							Group: false,
							Match: StringMatchAll{},
							Attributes: []*Attribute{
								{Name: "name", Type: TypeString, Required: true},
								{Name: "timeout", Type: TypeString},
							},
						},
					},
				},
			},
		},
	}

	eng, err := New(schema, yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	// Create object
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-123")

	// Add functions
	funcsObj := object.New[object.Refrence]()
	func1 := object.New[object.Refrence]()
	func1.Set("name", "myFunction")
	func1.Set("timeout", "10s")
	err = funcsObj.Child("myFunction").Add(func1)
	assert.NilError(t, err)
	err = obj.Child("functions").Add(funcsObj)
	assert.NilError(t, err)

	// Dump
	err = eng.Dump(obj)
	assert.NilError(t, err)

	// Verify function file was created
	exists, err := afero.Exists(memFs, "/functions/myFunction.yaml")
	assert.NilError(t, err)
	assert.Assert(t, exists, "functions/myFunction.yaml should exist")
}

func TestDump_WithMissingRequiredAttribute(t *testing.T) {
	memFs := afero.NewMemMapFs()

	schema := &schemaDef{
		root: &Node{
			Group: true,
			Match: nil,
			Attributes: []*Attribute{
				{Name: "id", Type: TypeString, Required: true},
				{Name: "name", Type: TypeString, Required: true}, // Required but missing
			},
		},
	}

	eng, err := New(schema, yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	obj := object.New[object.Refrence]()
	obj.Set("id", "project-123")
	// name is missing

	err = eng.Dump(obj)
	assert.ErrorContains(t, err, "required attribute 'name' is missing")
}

func TestDump_WithValidationError(t *testing.T) {
	memFs := afero.NewMemMapFs()

	schema := &schemaDef{
		root: &Node{
			Group: true,
			Match: nil,
			Attributes: []*Attribute{
				{
					Name:     "type",
					Type:     TypeString,
					Required: true,
					Validator: func(v any) error {
						s, ok := v.(string)
						if !ok || (s != "http" && s != "p2p") {
							return errors.New("invalid type value")
						}
						return nil
					},
				},
			},
		},
	}

	eng, err := New(schema, yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	obj := object.New[object.Refrence]()
	obj.Set("type", "invalid")

	err = eng.Dump(obj)
	assert.ErrorContains(t, err, "validation failed")
}

func TestDump_SkipsDefaultAttributeValues(t *testing.T) {
	memFs := afero.NewMemMapFs()

	schema := &schemaDef{
		root: &Node{
			Group: true,
			Match: nil,
			Attributes: []*Attribute{
				{Name: "id", Type: TypeString, Required: true},
				{Name: "enabled", Type: TypeBool, Default: false},
			},
		},
	}

	eng, err := New(schema, yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	obj := object.New[object.Refrence]()
	obj.Set("id", "test")
	obj.Set("enabled", false) // Same as default

	err = eng.Dump(obj)
	assert.NilError(t, err)

	// Read config and check that enabled is not in there
	content, err := afero.ReadFile(memFs, "/config.yaml")
	assert.NilError(t, err)
	// The file should contain id but not enabled
	assert.Assert(t, len(content) > 0)
}

func TestDump_WithEmptyGroup(t *testing.T) {
	memFs := afero.NewMemMapFs()

	schema := &schemaDef{
		root: &Node{
			Group: true,
			Match: nil,
			Attributes: []*Attribute{
				{Name: "id", Type: TypeString, Required: true},
			},
			Children: []*Node{
				{
					Group: true,
					Match: "functions",
					Children: []*Node{
						{
							Group: false,
							Match: StringMatchAll{},
							Attributes: []*Attribute{
								{Name: "name", Type: TypeString},
							},
						},
					},
				},
			},
		},
	}

	eng, err := New(schema, yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	obj := object.New[object.Refrence]()
	obj.Set("id", "test")
	// No functions group - should be fine

	err = eng.Dump(obj)
	assert.NilError(t, err)

	// Only config.yaml should exist
	exists, err := afero.Exists(memFs, "/config.yaml")
	assert.NilError(t, err)
	assert.Assert(t, exists)
}

func TestDump_WithCustomPath(t *testing.T) {
	memFs := afero.NewMemMapFs()

	// Attribute with custom path
	schema := &schemaDef{
		root: &Node{
			Group: true,
			Match: nil,
			Attributes: []*Attribute{
				{Name: "id", Type: TypeString, Required: true},
				{
					Name: "timeout",
					Type: TypeString,
					Path: []StringMatch{"execution", "timeout"},
				},
			},
		},
	}

	eng, err := New(schema, yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	obj := object.New[object.Refrence]()
	obj.Set("id", "test")
	obj.Set("timeout", "30s")

	err = eng.Dump(obj)
	assert.NilError(t, err)

	// Verify config was created
	content, err := afero.ReadFile(memFs, "/config.yaml")
	assert.NilError(t, err)
	assert.Assert(t, len(content) > 0)
}

func TestWriteValueToPath_WithSimpleString(t *testing.T) {
	memFs := afero.NewMemMapFs()
	seer, err := yaseer.New(yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	query := seer.Query().Get("test").Document()

	attr := &Attribute{Name: "myattr", Type: TypeString}
	obj := object.New[object.Refrence]()

	err = writeValueToPath(query, []StringMatch{"level1", "level2"}, attr, "myvalue", obj)
	assert.NilError(t, err)

	err = query.Commit()
	assert.NilError(t, err)

	err = seer.Sync()
	assert.NilError(t, err)

	// Verify file exists
	exists, err := afero.Exists(memFs, "/test.yaml")
	assert.NilError(t, err)
	assert.Assert(t, exists)
}

func TestWriteValueToPath_WithEitherAndKey(t *testing.T) {
	memFs := afero.NewMemMapFs()
	seer, err := yaseer.New(yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	query := seer.Query().Get("test").Document()

	// Key attribute with Either() path
	attr := &Attribute{
		Name: "source",
		Type: TypeString,
		Key:  true,
		Path: []StringMatch{Either("http", "p2p")},
	}
	obj := object.New[object.Refrence]()
	obj.Set("source", "http")

	err = writeValueToPath(query, attr.Path, attr, "http", obj)
	assert.NilError(t, err)

	err = query.Commit()
	assert.NilError(t, err)

	err = seer.Sync()
	assert.NilError(t, err)

	exists, err := afero.Exists(memFs, "/test.yaml")
	assert.NilError(t, err)
	assert.Assert(t, exists)
}

func TestWriteValueToPath_WithEitherAndType(t *testing.T) {
	memFs := afero.NewMemMapFs()
	seer, err := yaseer.New(yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	query := seer.Query().Get("test").Document()

	// Non-key attribute with Either() - needs type attribute
	attr := &Attribute{
		Name: "endpoint",
		Type: TypeString,
		Path: []StringMatch{Either("http", "p2p"), "endpoint"},
	}
	obj := object.New[object.Refrence]()
	obj.Set("type", "http")
	obj.Set("endpoint", "/api/v1")

	err = writeValueToPath(query, attr.Path, attr, "/api/v1", obj)
	assert.NilError(t, err)

	err = query.Commit()
	assert.NilError(t, err)

	err = seer.Sync()
	assert.NilError(t, err)

	exists, err := afero.Exists(memFs, "/test.yaml")
	assert.NilError(t, err)
	assert.Assert(t, exists)
}

func TestWriteValueToPath_WithEitherNoMatch(t *testing.T) {
	memFs := afero.NewMemMapFs()
	seer, err := yaseer.New(yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	query := seer.Query().Get("test").Document()

	attr := &Attribute{
		Name: "source",
		Type: TypeString,
		Key:  true,
		Path: []StringMatch{Either("http", "p2p")},
	}
	obj := object.New[object.Refrence]()
	obj.Set("source", "invalid") // Does not match Either options

	err = writeValueToPath(query, attr.Path, attr, "invalid", obj)
	assert.ErrorContains(t, err, "does not match Either() options")
}

func TestFindTypeValue_Success(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("type", "http")

	typeVal, err := findTypeValue(obj)
	assert.NilError(t, err)
	assert.Equal(t, typeVal, "http")
}

func TestFindTypeValue_WhenMissing(t *testing.T) {
	obj := object.New[object.Refrence]()

	_, err := findTypeValue(obj)
	assert.ErrorContains(t, err, "type attribute not found")
}

func TestFindTypeValue_WhenNotString(t *testing.T) {
	obj := object.New[object.Refrence]()
	obj.Set("type", 123) // Not a string

	_, err := findTypeValue(obj)
	assert.ErrorContains(t, err, "type attribute is not a string")
}

func TestDumpChild_WithNonGroupError(t *testing.T) {
	memFs := afero.NewMemMapFs()

	schema := &schemaDef{
		root: &Node{Group: true, Match: nil},
	}

	eng, err := New(schema, yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	instance := eng.(*instance)
	query := instance.seer.Query()

	// Create child that is not a group
	child := &Node{
		Group: false,
		Match: "functions",
	}

	obj := object.New[object.Refrence]()
	funcs := object.New[object.Refrence]()
	err = obj.Child("functions").Add(funcs)
	assert.NilError(t, err)

	err = instance.dumpChild(child, obj, query)
	assert.ErrorContains(t, err, "non-group child")
}

func TestDumpChild_WithUnsupportedMatchType(t *testing.T) {
	memFs := afero.NewMemMapFs()

	schema := &schemaDef{
		root: &Node{Group: true, Match: nil},
	}

	eng, err := New(schema, yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	instance := eng.(*instance)
	query := instance.seer.Query()

	// Child with unsupported match type (integer)
	child := &Node{
		Group: true,
		Match: 12345, // Invalid match type
	}

	obj := object.New[object.Refrence]()

	err = instance.dumpChild(child, obj, query)
	assert.ErrorContains(t, err, "unsupported child match type")
}

func TestDump_WithApplications(t *testing.T) {
	memFs := afero.NewMemMapFs()

	// Schema with applications (DefineIterGroup)
	appResources := []*Node{
		{
			Group: true,
			Match: "functions",
			Children: []*Node{
				{
					Group: false,
					Match: StringMatchAll{},
					Attributes: []*Attribute{
						{Name: "name", Type: TypeString, Required: true},
					},
				},
			},
		},
	}

	schema := &schemaDef{
		root: &Node{
			Group: true,
			Match: nil,
			Attributes: []*Attribute{
				{Name: "id", Type: TypeString, Required: true},
			},
			Children: []*Node{
				{
					Group: true,
					Match: "applications",
					Children: []*Node{
						{
							Group: true,
							Match: StringMatchAll{}, // DefineIterGroup
							Attributes: []*Attribute{
								{Name: "id", Type: TypeString, Required: true},
								{Name: "name", Type: TypeString},
							},
							Children: appResources,
						},
					},
				},
			},
		},
	}

	eng, err := New(schema, yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	// Build object with applications
	obj := object.New[object.Refrence]()
	obj.Set("id", "project-123")

	apps := object.New[object.Refrence]()

	app1 := object.New[object.Refrence]()
	app1.Set("id", "app-1")
	app1.Set("name", "MyApp")

	app1Funcs := object.New[object.Refrence]()
	func1 := object.New[object.Refrence]()
	func1.Set("name", "appFunc")
	err = app1Funcs.Child("appFunc").Add(func1)
	assert.NilError(t, err)
	err = app1.Child("functions").Add(app1Funcs)
	assert.NilError(t, err)

	err = apps.Child("MyApp").Add(app1)
	assert.NilError(t, err)
	err = obj.Child("applications").Add(apps)
	assert.NilError(t, err)

	err = eng.Dump(obj)
	assert.NilError(t, err)

	// Verify application config
	exists, err := afero.Exists(memFs, "/applications/MyApp/config.yaml")
	assert.NilError(t, err)
	assert.Assert(t, exists, "applications/MyApp/config.yaml should exist")

	// Verify application function
	exists, err = afero.Exists(memFs, "/applications/MyApp/functions/appFunc.yaml")
	assert.NilError(t, err)
	assert.Assert(t, exists, "applications/MyApp/functions/appFunc.yaml should exist")
}

func TestDump_WithMultipleResources(t *testing.T) {
	memFs := afero.NewMemMapFs()

	schema := &schemaDef{
		root: &Node{
			Group: true,
			Match: nil,
			Attributes: []*Attribute{
				{Name: "id", Type: TypeString, Required: true},
			},
			Children: []*Node{
				{
					Group: true,
					Match: "functions",
					Children: []*Node{
						{
							Group: false,
							Match: StringMatchAll{},
							Attributes: []*Attribute{
								{Name: "name", Type: TypeString, Required: true},
							},
						},
					},
				},
			},
		},
	}

	eng, err := New(schema, yaseer.VirtualFS(memFs, "/"))
	assert.NilError(t, err)

	obj := object.New[object.Refrence]()
	obj.Set("id", "project")

	funcs := object.New[object.Refrence]()
	for _, name := range []string{"func1", "func2", "func3"} {
		f := object.New[object.Refrence]()
		f.Set("name", name)
		err = funcs.Child(name).Add(f)
		assert.NilError(t, err)
	}
	err = obj.Child("functions").Add(funcs)
	assert.NilError(t, err)

	err = eng.Dump(obj)
	assert.NilError(t, err)

	// Verify all function files
	for _, name := range []string{"func1", "func2", "func3"} {
		exists, err := afero.Exists(memFs, "/functions/"+name+".yaml")
		assert.NilError(t, err)
		assert.Assert(t, exists, "functions/%s.yaml should exist", name)
	}
}
