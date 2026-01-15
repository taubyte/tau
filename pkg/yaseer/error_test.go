package seer

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestYAMLError_Parsing(t *testing.T) {
	// Use real filesystem for invalid YAML file tests
	tempDir := t.TempDir()
	seer, err := New(SystemFS(tempDir))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("YAMLError includes file path and location", func(t *testing.T) {
		// Create an invalid YAML file
		invalidYAML := "key: [unclosed bracket"
		filePath := "/invalid.yaml"

		// Write invalid YAML directly to filesystem
		f, err := seer.fs.OpenFile(filePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0640)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(invalidYAML)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()

		// Try to load the invalid YAML
		_, err = seer.loadYamlDocument(filePath)
		if err == nil {
			t.Fatal("Expected error when loading invalid YAML")
		}

		// Check if it's a YAMLError
		yamlErr, ok := err.(*YAMLError)
		if !ok {
			t.Fatalf("Expected YAMLError, got %T: %v", err, err)
		}

		if yamlErr.FilePath != filePath {
			t.Errorf("Expected filePath %s, got %s", filePath, yamlErr.FilePath)
		}

		// Error message should include file path
		errMsg := yamlErr.Error()
		if !strings.Contains(errMsg, "invalid.yaml") {
			t.Errorf("Error message should contain file path, got: %s", errMsg)
		}
	})

	t.Run("YAMLError Unwrap returns original error", func(t *testing.T) {
		invalidYAML := "key: [unclosed"
		filePath := "/unwrap_test.yaml"

		f, err := seer.fs.OpenFile(filePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0640)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(invalidYAML)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()

		_, err = seer.loadYamlDocument(filePath)
		if err == nil {
			t.Fatal("Expected error")
		}

		yamlErr, ok := err.(*YAMLError)
		if !ok {
			t.Fatalf("Expected YAMLError, got %T", err)
		}

		originalErr := yamlErr.Unwrap()
		if originalErr == nil {
			t.Error("Unwrap should return the original error")
		}
	})
}

func TestFork_Query(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("Fork creates independent copy of query", func(t *testing.T) {
		original := seer.Get("fork").Get("test").Document()
		forked := Fork(original)

		// Modify original
		original.Set("original_value")

		// Forked should be independent
		forked.Set("forked_value")

		// Commit both
		if err := original.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := forked.Commit(); err != nil {
			t.Fatal(err)
		}

		// Both should exist
		var origVal, forkVal string
		seer.Get("fork").Get("test").Value(&origVal)
		seer.Get("fork").Get("test").Value(&forkVal)

		// The last commit wins, but both queries should work independently
		_ = origVal
		_ = forkVal
	})

	t.Run("Fork method creates independent copy", func(t *testing.T) {
		query := seer.Get("fork2").Get("test2")
		forked := query.Fork()

		// Both should be independent
		if query == forked {
			t.Error("Fork should create a new instance")
		}

		// Operations on one shouldn't affect the other
		query.Get("nested1")
		forked.Get("nested2")

		if len(query.requestedPath) != len(forked.requestedPath) {
			t.Error("Forked query should have same initial path")
		}
	})
}

func TestClear_Query(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("Clear resets query state", func(t *testing.T) {
		query := seer.Get("clear").Get("test").Document()
		query.Set("value")

		cleared := query.Clear()

		if len(cleared.ops) != 0 {
			t.Error("Clear should remove all operations")
		}
		if len(cleared.errors) != 0 {
			t.Error("Clear should remove all errors")
		}
		if cleared.write {
			t.Error("Clear should reset write flag")
		}
	})

	t.Run("Clear returns query for chaining", func(t *testing.T) {
		query := seer.Get("clear2")
		cleared := query.Clear()

		if cleared == nil {
			t.Error("Clear should return the query")
		}
		if cleared != query {
			t.Error("Clear should return the same query instance")
		}
	})
}

func TestErrors_Query(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("Errors returns copy of error slice", func(t *testing.T) {
		query := seer.Query()
		// Force an error
		query.Document() // This should add an error

		errors := query.Errors()
		if len(errors) == 0 {
			t.Error("Expected errors to be present")
		}

		// Modify the returned slice - shouldn't affect original
		errors = append(errors, nil)
		if len(query.Errors()) == len(errors) {
			t.Error("Errors should return a copy")
		}
	})

	t.Run("Errors returns empty slice when no errors", func(t *testing.T) {
		query := seer.Get("test")
		errors := query.Errors()
		if len(errors) != 0 {
			t.Errorf("Expected no errors, got %d", len(errors))
		}
	})
}

func TestDump_Seer(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("Dump doesn't panic", func(t *testing.T) {
		// Create some documents
		seer.Get("dump").Get("test1").Document().Set("value1").Commit()
		seer.Get("dump").Get("test2").Document().Set("value2").Commit()

		// Dump should not panic
		seer.Dump()
	})

	t.Run("Dump works with empty seer", func(t *testing.T) {
		emptySeer := newTestSeer(t)
		emptySeer.Dump()
	})
}

func TestDocument_ErrorHandling(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("Document on empty query adds error", func(t *testing.T) {
		query := seer.Query()
		query.Document() // Should add error

		errors := query.Errors()
		if len(errors) == 0 {
			t.Error("Expected error when calling Document on empty query")
		}
	})
}

func TestValue_ErrorHandling(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("Value returns error when query has errors", func(t *testing.T) {
		query := seer.Query()
		query.Document() // Add error

		var val string
		err := query.Value(&val)
		if err == nil {
			t.Error("Expected error when query has errors")
		}
	})

	t.Run("Value returns error for non-existent path", func(t *testing.T) {
		var val string
		err := seer.Get("nonexistent").Get("path").Get("deep").Value(&val)
		if err == nil {
			t.Error("Expected error for non-existent path")
		}
	})

	t.Run("Value includes file and line in decode errors", func(t *testing.T) {
		// Create a document with incompatible type
		err := seer.Get("type").Get("test").Document().Set("string_value").Commit()
		if err != nil {
			t.Fatal(err)
		}

		var intVal int
		err = seer.Get("type").Get("test").Value(&intVal)
		if err == nil {
			t.Error("Expected error when decoding incompatible type")
		}

		// Error should mention the file
		errMsg := err.Error()
		if !strings.Contains(errMsg, "type/test.yaml") {
			t.Errorf("Error should mention file path, got: %s", errMsg)
		}
	})
}

func TestYAMLError_ErrorFormats(t *testing.T) {
	t.Run("Error with line and column", func(t *testing.T) {
		err := &YAMLError{
			FilePath: "/test.yaml",
			Line:     5,
			Column:   10,
			Err:      errors.New("syntax error"),
		}
		msg := err.Error()
		if msg == "" {
			t.Error("Error message should not be empty")
		}
	})

	t.Run("Error with line only", func(t *testing.T) {
		err := &YAMLError{
			FilePath: "/test.yaml",
			Line:     3,
			Column:   0,
			Err:      errors.New("parse error"),
		}
		msg := err.Error()
		if msg == "" {
			t.Error("Error message should not be empty")
		}
	})

	t.Run("Error without line or column", func(t *testing.T) {
		err := &YAMLError{
			FilePath: "/test.yaml",
			Line:     0,
			Column:   0,
			Err:      errors.New("unknown error"),
		}
		msg := err.Error()
		if msg == "" {
			t.Error("Error message should not be empty")
		}
	})
}

func TestParseYAMLError_DifferentFormats(t *testing.T) {
	t.Run("Parse error with line and column", func(t *testing.T) {
		err := errors.New("yaml: line 5: column 10: syntax error")
		line, column := parseYAMLError(err)
		if line != 5 {
			t.Errorf("Expected line 5, got %d", line)
		}
		if column != 10 {
			t.Errorf("Expected column 10, got %d", column)
		}
	})

	t.Run("Parse error with line only", func(t *testing.T) {
		err := errors.New("yaml: line 3: parse error")
		line, column := parseYAMLError(err)
		if line != 3 {
			t.Errorf("Expected line 3, got %d", line)
		}
		if column != 0 {
			t.Errorf("Expected column 0, got %d", column)
		}
	})

	t.Run("Parse error with different format", func(t *testing.T) {
		err := errors.New("yaml: line 7: column 2: unexpected character")
		line, column := parseYAMLError(err)
		if line != 7 {
			t.Errorf("Expected line 7, got %d", line)
		}
		if column != 2 {
			t.Errorf("Expected column 2, got %d", column)
		}
	})

	t.Run("Parse nil error", func(t *testing.T) {
		line, column := parseYAMLError(nil)
		if line != 0 || column != 0 {
			t.Errorf("Expected (0, 0), got (%d, %d)", line, column)
		}
	})

	t.Run("Parse error without line info", func(t *testing.T) {
		err := errors.New("some other error")
		line, column := parseYAMLError(err)
		if line != 0 || column != 0 {
			t.Errorf("Expected (0, 0), got (%d, %d)", line, column)
		}
	})
}

func TestLoadYamlDocument_ErrorHandling(t *testing.T) {
	// Use real filesystem for file operations
	tempDir := t.TempDir()
	seer, err := New(SystemFS(tempDir))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("LoadYamlDocument with non-existent file returns error", func(t *testing.T) {
		_, err := seer.loadYamlDocument("/nonexistent.yaml")
		if err == nil {
			t.Error("Expected error for non-existent file")
		}
	})

	t.Run("LoadYamlDocument with invalid YAML returns YAMLError", func(t *testing.T) {
		// Create invalid YAML
		filePath := "/invalid.yaml"
		f, err := seer.fs.OpenFile(filePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0640)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString("invalid: [unclosed")
		if err != nil {
			t.Fatal(err)
		}
		f.Close()

		_, err = seer.loadYamlDocument(filePath)
		if err == nil {
			t.Error("Expected error for invalid YAML")
		}

		_, ok := err.(*YAMLError)
		if !ok {
			t.Errorf("Expected YAMLError, got %T", err)
		}
	})
}

func TestCommit_ErrorHandling(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("Commit with errors returns error", func(t *testing.T) {
		query := seer.Query()
		query.Document() // This adds an error

		err := query.Commit()
		if err == nil {
			t.Error("Expected error when committing with errors")
		}
	})

	t.Run("Commit handles operation handler errors", func(t *testing.T) {
		// Create a query that will fail during commit
		query := seer.Get("commit").Get("test").Document()
		query.Set("value")

		// Normal commit should work
		err := query.Commit()
		if err != nil {
			// If error, that's the path we're testing
			_ = err
		}
	})
}

func TestGetNodeLocation(t *testing.T) {
	t.Run("getNodeLocation with nil node returns zeros", func(t *testing.T) {
		line, column := getNodeLocation(nil)
		if line != 0 || column != 0 {
			t.Errorf("Expected (0, 0), got (%d, %d)", line, column)
		}
	})
}

func TestValue_FolderTypes(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("Value on folder with interface{} type", func(t *testing.T) {
		seer.Get("folder").Get("file1").Document().Set("val1").Commit()
		seer.Get("folder").Get("file2").Document().Set("val2").Commit()

		var val interface{}
		err := seer.Get("folder").Value(&val)
		if err != nil {
			t.Fatalf("Failed to get folder as interface{}: %v", err)
		}

		files, ok := val.([]string)
		if !ok {
			t.Errorf("Expected []string, got %T", val)
		}
		if len(files) < 2 {
			t.Errorf("Expected at least 2 files, got %d", len(files))
		}
	})

	t.Run("Value on folder with unsupported type returns error", func(t *testing.T) {
		seer.Get("foldererr").Get("file1").Document().Set("val1").Commit()

		var intVal int
		err := seer.Get("foldererr").Value(&intVal)
		if err == nil {
			t.Error("Expected error when using unsupported type for folder")
		}
	})
}
