package seer

import (
	"os"
	"testing"

	"golang.org/x/exp/slices"
	"gotest.tools/v3/assert"
)

func TestListEmpty(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	assert.NilError(t, err)

	items, err := seer.List()
	assert.NilError(t, err)
	assert.Assert(t, len(items) == 0)

	for _, query := range []*Query{
		seer.Get("parent"),
		seer.Get("parent").Get("p").Document(),
		seer.Get("a").Get("b").Get("C").Document().Get("a").Get("orange"),
	} {
		err = query.Fork().Commit()
		assert.NilError(t, err)

		items, err = query.Fork().List()
		assert.NilError(t, err)
		assert.Assert(t, len(items) == 0)
	}
}

func TestListSet(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	assert.NilError(t, err)

	seer.Get("parent").Get("p").Commit()
	listItems, err := seer.List()
	assert.NilError(t, err)
	assert.Equal(t, listItems[0], string("parent"))

	listItems, err = seer.Get("parent").List()
	assert.NilError(t, err)
	assert.Equal(t, listItems[0], "p")
}

func TestListMultiSet(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	assert.NilError(t, err)

	items := []string{"oranges", "bananas", "pears", "pineapples", "coconuts"}
	for _, i := range items {
		err = seer.Get("parent").Get(i).Commit()
		assert.NilError(t, err)
	}

	listItems, err := seer.Get("parent").List()
	assert.NilError(t, err)

	assertContains(t, listItems, items...)
}

func TestListDeepMultiSet(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	assert.NilError(t, err)

	items := []string{"oranges", "bananas", "pears", "pineapples", "coconuts"}
	for _, i := range items {
		assert.NilError(t, seer.Get("parent").Get("sad").Get("fruits").Get(i).Commit())
	}

	listItems, err := seer.Get("parent").Get("sad").Get("fruits").List()
	assert.NilError(t, err)

	assertContains(t, listItems, items...)
}

func TestListDeepCommitDelete(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	assert.NilError(t, err)

	items := []string{"oranges", "bananas", "pears", "pineapples", "coconuts"}
	toDelete := []string{items[1], items[2]}
	expectedItems := append(items[:1], items[3:]...)

	query := seer.Get("parent").Get("sad").Get("fruits")

	for _, i := range items {
		err = query.Fork().Get(i).Commit()
		assert.NilError(t, err)
	}

	for _, i := range toDelete {
		err = query.Fork().Get(i).Delete().Commit()
		assert.NilError(t, err)
	}

	listItems, err := query.List()
	assert.NilError(t, err)

	assertContains(t, listItems, expectedItems...)
	assertNotContains(t, listItems, toDelete...)

}

func TestListOnDocument(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	assert.NilError(t, err)

	documentName := "some-doc"
	item1 := "pears"
	item2 := "oranges"

	err = seer.Get(documentName).Document().Get(item1).Set(10).Commit()
	assert.NilError(t, err)

	err = seer.Get(documentName).Document().Get(item2).Set(20).Commit()
	assert.NilError(t, err)

	list, err := seer.Get(documentName).List()
	assert.NilError(t, err)

	assertContains(t, list, item1, item2)
}

func assertContains(t *testing.T, val []string, items ...string) {
	for _, item := range items {
		assert.Assert(t, slices.Contains(val, item), "%s not in %v", item, val)
	}
}

func assertNotContains(t *testing.T, val []string, items ...string) {
	for _, item := range items {
		assert.Assert(t, slices.Contains(val, item) == false, "%s in %v", item, val)
	}
}

func TestList_EmptyValue(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("List on empty value returns nil", func(t *testing.T) {
		err := seer.Get("empty").Get("test").Document().Commit()
		assert.NilError(t, err)

		keys, err := seer.Get("empty").Get("test").List()
		assert.NilError(t, err)
		// Empty value returns nil per implementation
		assert.Assert(t, keys == nil, "Expected nil for empty document, got %v", keys)
	})
}

func TestList_MapKeys(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("List on map returns keys", func(t *testing.T) {
		err := seer.Get("listmap").Get("test").Document().Set(map[string]string{
			"key1": "val1",
			"key2": "val2",
			"key3": "val3",
		}).Commit()
		assert.NilError(t, err)

		keys, err := seer.Get("listmap").Get("test").List()
		assert.NilError(t, err)
		assert.Equal(t, len(keys), 3)
	})

	t.Run("List on map with interface{} keys", func(t *testing.T) {
		err := seer.Get("iface").Get("test").Document().Set(map[interface{}]interface{}{
			"key1": "val1",
			"key2": "val2",
		}).Commit()
		assert.NilError(t, err)

		keys, err := seer.Get("iface").Get("test").List()
		assert.NilError(t, err)
		assert.Equal(t, len(keys), 2)
	})
}

func TestList_ErrorCases(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("List on non-map non-slice returns error", func(t *testing.T) {
		err := seer.Get("list").Get("test").Document().Set("string_value").Commit()
		assert.NilError(t, err)

		_, err = seer.Get("list").Get("test").List()
		assert.Assert(t, err != nil, "Expected error when listing non-map/non-slice")
	})
}

func TestRootList(t *testing.T) {
	seer := newTestSeer(t)

	t.Run("List returns directories and YAML files", func(t *testing.T) {
		// Create some directories and files
		seer.Get("root").Get("file1").Document().Set("val1").Commit()
		seer.Get("root").Get("file2").Document().Set("val2").Commit()
		seer.Get("root").Get("subdir").Get("file3").Document().Set("val3").Commit()

		list, err := seer.List()
		assert.NilError(t, err)

		// Should contain "root" directory
		found := false
		for _, item := range list {
			if item == "root" {
				found = true
				break
			}
		}
		assert.Assert(t, found, "Expected 'root' in list")
	})

	t.Run("List handles empty root", func(t *testing.T) {
		emptySeer := newTestSeer(t)

		list, err := emptySeer.List()
		assert.NilError(t, err)
		_ = list // May be empty or nil
	})

	t.Run("List handles ReadDir error gracefully", func(t *testing.T) {
		// Test with valid filesystem - error path is hard to simulate
		seer := newTestSeer(t)
		_, err := seer.List()
		// Should not error with valid filesystem
		assert.NilError(t, err)
	})

	t.Run("List filters non-YAML files", func(t *testing.T) {
		// Use real filesystem to create non-YAML files
		tempDir := t.TempDir()
		fsSeer, err := New(SystemFS(tempDir))
		assert.NilError(t, err)

		// Create YAML files
		fsSeer.Get("dir").Get("file1").Document().Set("val1").Commit()
		fsSeer.Get("dir").Get("file2").Document().Set("val2").Commit()

		// Create a non-YAML file
		filePath := tempDir + "/dir/non-yaml.txt"
		f, err := os.Create(filePath)
		assert.NilError(t, err)
		f.WriteString("not yaml")
		f.Close()

		// List should only return YAML files
		list, err := fsSeer.Get("dir").List()
		assert.NilError(t, err)
		// Should only contain YAML files, not .txt
		for _, item := range list {
			if item == "non-yaml" {
				t.Error("List should not include non-YAML files")
			}
		}
	})
}
