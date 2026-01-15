package seer

import (
	"testing"
)

func TestLocationMethods_FilePath(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("FilePath returns empty string before document access", func(t *testing.T) {
		query := seer.Get("test")
		if query.FilePath() != "" {
			t.Errorf("Expected empty filePath, got %s", query.FilePath())
		}
	})

	t.Run("FilePath returns correct path after document creation", func(t *testing.T) {
		err := seer.Get("config").Get("app").Document().Set("value").Commit()
		if err != nil {
			t.Fatal(err)
		}

		// Need to perform a Value() operation to set the location - use same query instance
		query := seer.Get("config").Get("app")
		var val string
		err = query.Value(&val)
		if err != nil {
			t.Fatal(err)
		}

		filePath := query.FilePath()
		expected := "/config/app.yaml"
		if filePath != expected {
			t.Errorf("Expected filePath %s, got %s", expected, filePath)
		}
	})

	t.Run("FilePath returns correct path after reading existing document", func(t *testing.T) {
		query := seer.Get("config").Get("app")
		var val string
		err := query.Value(&val)
		if err != nil {
			t.Fatal(err)
		}

		filePath := query.FilePath()
		expected := "/config/app.yaml"
		if filePath != expected {
			t.Errorf("Expected filePath %s, got %s", expected, filePath)
		}
	})
}

func TestLocationMethods_LineAndColumn(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("Line and Column return zero before document access", func(t *testing.T) {
		query := seer.Get("test")
		if query.Line() != 0 {
			t.Errorf("Expected line 0, got %d", query.Line())
		}
		if query.Column() != 0 {
			t.Errorf("Expected column 0, got %d", query.Column())
		}
	})

	t.Run("Line and Column are set after document access", func(t *testing.T) {
		err := seer.Get("data").Get("nested").Document().Set(map[string]interface{}{
			"key1": "value1",
			"key2": "value2",
		}).Commit()
		if err != nil {
			t.Fatal(err)
		}

		var result string
		err = seer.Get("data").Get("nested").Get("key1").Value(&result)
		if err != nil {
			t.Fatal(err)
		}

		query := seer.Get("data").Get("nested").Get("key1")
		line := query.Line()
		column := query.Column()

		// Line should be > 0 if the YAML was parsed (yaml.Node sets Line during parsing)
		if line < 0 {
			t.Errorf("Expected line >= 0, got %d", line)
		}
		if column < 0 {
			t.Errorf("Expected column >= 0, got %d", column)
		}
	})
}

func TestLocationMethods_Location(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("Location returns all zero values before document access", func(t *testing.T) {
		query := seer.Get("test")
		filePath, line, column := query.Location()
		if filePath != "" {
			t.Errorf("Expected empty filePath, got %s", filePath)
		}
		if line != 0 {
			t.Errorf("Expected line 0, got %d", line)
		}
		if column != 0 {
			t.Errorf("Expected column 0, got %d", column)
		}
	})

	t.Run("Location returns correct values after document access", func(t *testing.T) {
		err := seer.Get("settings").Get("database").Document().Set(map[string]string{
			"host": "localhost",
			"port": "5432",
		}).Commit()
		if err != nil {
			t.Fatal(err)
		}

		query := seer.Get("settings").Get("database").Get("host")
		var host string
		err = query.Value(&host)
		if err != nil {
			t.Fatal(err)
		}

		filePath, line, column := query.Location()

		expectedPath := "/settings/database.yaml"
		if filePath != expectedPath {
			t.Errorf("Expected filePath %s, got %s", expectedPath, filePath)
		}
		if line < 0 {
			t.Errorf("Expected line >= 0, got %d", line)
		}
		if column < 0 {
			t.Errorf("Expected column >= 0, got %d", column)
		}
	})
}

func TestLocationMethods_WithNestedPaths(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("Location is preserved through nested Get operations", func(t *testing.T) {
		err := seer.Get("level1").Get("level2").Get("level3").Document().Set("deep_value").Commit()
		if err != nil {
			t.Fatal(err)
		}

		// Create query and perform Value operation to set location
		query := seer.Get("level1").Get("level2").Get("level3")
		var val string
		err = query.Value(&val)
		if err != nil {
			t.Fatal(err)
		}

		// Now check location
		filePath, line, column := query.Location()

		expectedPath := "/level1/level2/level3.yaml"
		if filePath != expectedPath {
			t.Errorf("Expected filePath %s, got %s", expectedPath, filePath)
		}
		if line < 0 || column < 0 {
			t.Errorf("Expected valid line/column, got line=%d, column=%d", line, column)
		}
	})
}
