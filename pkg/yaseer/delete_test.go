package seer

import (
	"fmt"
	"testing"

	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
	"gotest.tools/v3/assert"
)

func _setDelete(seer *Seer, path string, inner string, value interface{}) error {
	seer.Get(path).Document().Get(inner).Set(value).Commit()
	err := seer.Get(path).Get(inner).Delete().Commit()
	if err != nil {
		return fmt.Errorf("delete failed with error: %s", err.Error())
	}
	var val yaml.Node
	err = seer.Get(path).Get(inner).Value(&val)

	if err == nil {
		return fmt.Errorf("FAILMSG: Should return errror")

	}
	return nil
}

func _setDeleteStringItems(seer *Seer, path string, inner string, items []string) error {
	seer.Get(path).Document().Get(inner).Set(items).Commit()
	seer.Get(path).Get(inner).Delete().Commit()
	val := make([]string, 0)
	seer.Get(path).Get(inner).Value(&val)
	for _, v := range val {
		if slices.Contains(items, v) == false {
			return fmt.Errorf("FAILMSG: %s not in %s", v, items)
		}
	}
	return nil
}

func _setDeleteMap(seer *Seer, path string, inner string, items map[string]string) error {
	seer.Get(path).Document().Get(inner).Set(items).Commit()
	err := seer.Get(path).Get(inner).Delete().Commit()
	if err != nil {
		return fmt.Errorf("FAILMSG: for `%s/%s` failed with %w should be empty", path, inner, err)
	}

	val := make(map[string]string)
	seer.Get(path).Get(inner).Value(&val)
	if len(val) != 0 {
		return fmt.Errorf("FAILMSG: for `%s/%s` %v should be empty", path, inner, val)
	}
	return nil
}

func TestDelete(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	if err != nil {
		t.Error(err)
		return
	}
	t.Parallel()

	t.Run("set then delete string and get", func(t *testing.T) {
		err := seer.Get("parent").Get("p").Document().Set("hello").Commit()
		if err != nil {
			t.Errorf("set failed with error: %s", err.Error())
		}
		var val string
		if seer.Get("parent").Get("p").Delete().Commit() != nil {
			t.Error("delete failed")
			return
		}

		if val == "hello" {
			t.Error("value is not nil")
			return
		}
	})

	t.Run("set int and get 1/2", func(t *testing.T) {
		err := _setDelete(seer, "parent1", "1", 1)
		assert.NilError(t, err)

		err = _setDelete(seer, "parent2", "1", 15)
		assert.NilError(t, err)
	})

	t.Run("set int and get 2/2", func(t *testing.T) {
		err := _setDelete(seer, "parent3", "1", 432145)
		assert.NilError(t, err)

		err = _setDelete(seer, "parent4", "1", 412655511)
		assert.NilError(t, err)

		err = _setDelete(seer, "parent5", "1", 97653436)
		assert.NilError(t, err)
	})

	t.Run("set float and get", func(t *testing.T) {
		err := _setDelete(seer, "parent1", "2", 1.1412948)
		assert.NilError(t, err)

		err = _setDelete(seer, "parent2", "2", 41241.4124912)
		assert.NilError(t, err)

		err = _setDelete(seer, "parent3", "2", 59891503.85629321)
		assert.NilError(t, err)

		err = _setDelete(seer, "parent4", "2", 18956896.75479195312)
		assert.NilError(t, err)
	})

	t.Run("set map and get 1/3", func(t *testing.T) {
		err := _setDeleteMap(seer, "parent1", "6", map[string]string{"hello": "world", "apple": "orange"})
		assert.NilError(t, err)
	})

	t.Run("set map and get 2/3", func(t *testing.T) {
		err := _setDeleteMap(seer, "parent2", "7", map[string]string{"dasddwa": "wordwadld", "dwadwaqqew": "dasdasdwaw"})
		assert.NilError(t, err)
	})

	t.Run("set map and get 3/3", func(t *testing.T) {
		err := _setDeleteMap(seer, "parent3", "9", map[string]string{"t": "wordwadld", "r": "dasdasdwaw"})
		assert.NilError(t, err)
	})

	t.Run("set bool and get", func(t *testing.T) {
		err := _setDelete(seer, "parent1", "3", true)
		assert.NilError(t, err)

		err = _setDelete(seer, "parent2", "3", false)
		assert.NilError(t, err)
	})

	t.Run("set string and get", func(t *testing.T) {
		err := _setDelete(seer, "parent1", "4", "somestring")
		assert.NilError(t, err)

		err = _setDelete(seer, "parent2", "4", "some\ttab string odd")
		assert.NilError(t, err)

		err = _setDelete(seer, "parent3", "4", "some \n string with newline")
		assert.NilError(t, err)

		err = _setDelete(seer, "parent4", "4", "some 84921 numbered \t odd \n string")
		assert.NilError(t, err)
	})

	t.Run("set array and get", func(t *testing.T) {
		err := _setDeleteStringItems(seer, "parent1", "5", []string{"hello", "apple", "orange"})
		assert.NilError(t, err)

		err = _setDeleteStringItems(seer, "parent2", "5", []string{"hello", "apple", "coconuts", "ora4214421nge"})
		assert.NilError(t, err)
	})

	t.Run("set map and get", func(t *testing.T) {
		err := _setDeleteMap(seer, "parent1", "6", map[string]string{"hello": "world", "apple": "orange"})
		assert.NilError(t, err)

		err = _setDeleteMap(seer, "parent2", "7", map[string]string{"dasddwa": "wordwadld", "dwadwaqqew": "dasdasdwaw"})
		assert.NilError(t, err)
	})
}
