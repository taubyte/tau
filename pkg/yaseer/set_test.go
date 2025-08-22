package seer

import (
	"testing"

	"golang.org/x/exp/slices"
)

func TestSet(t *testing.T) {
	seer, err := New(fixtureFS(true, "/"))
	if err != nil {
		t.Error(err)
		return
	}
	t.Run("set string and get", func(t *testing.T) {
		err := seer.Get("parent").Get("p").Document().Set("hello").Commit()
		if err != nil {
			t.Errorf("set failed with error: %s", err.Error())
		}
		var val string
		if seer.Get("parent").Get("p").Value(&val) != nil {
			t.Error("get failed")
			return
		}

		if val != "hello" {
			t.Error("value is not hello")
			return
		}
	})
	_set := func(t *testing.T, path string, inner string, value interface{}) {
		err := seer.Get(path).Document().Get(inner).Set(value).Commit()
		if err != nil {
			t.Errorf("set failed with error: %s", err.Error())
		}
		var val interface{}
		seer.Get(path).Get(inner).Value(&val)

		if val != value {
			t.Errorf("FAILMSG: %s not in %s", val, value)
			return
		}
	}

	_setStringItems := func(t *testing.T, path string, inner string, items []string) {
		seer.Get(path).Document().Get(inner).Set(items).Commit()
		var val []string
		seer.Get(path).Get(inner).Value(&val)
		for _, v := range val {
			if slices.Contains(items, v) == false {
				t.Errorf("FAILMSG: %s not in %s", v, items)
				return
			}
		}
	}

	_setMap := func(t *testing.T, path string, inner string, items map[string]string) {
		seer.Get(path).Document().Get(inner).Set(items).Commit()
		var val map[string]string
		seer.Get(path).Get(inner).Value(&val)
		for _, v := range val {
			if _, ok := items[v]; ok {
				t.Errorf("FAILMSG: %s not in %s", v, items)
				return
			}
		}
	}

	toRun2D := map[string][]func(t *testing.T){
		"set int and get": {
			func(t *testing.T) { _set(t, "parent1", "1", 1) },
			func(t *testing.T) { _set(t, "parent2", "1", 15) },
			func(t *testing.T) { _set(t, "parent3", "1", 432145) },
			func(t *testing.T) { _set(t, "parent4", "1", 412655511) },
			func(t *testing.T) { _set(t, "parent5", "1", 97653436) },
		},
		"set float and get": {
			func(t *testing.T) { _set(t, "parent1", "2", 1.1412948) },
			func(t *testing.T) { _set(t, "parent2", "2", 41241.4124912) },
			func(t *testing.T) { _set(t, "parent3", "2", 59891503.85629321) },
			func(t *testing.T) { _set(t, "parent4", "2", 18956896.75479195312) },
		},
		"set bool and get": {
			func(t *testing.T) { _set(t, "parent1", "3", true) },
			func(t *testing.T) { _set(t, "parent2", "3", false) },
		},
		"set string and get": {
			func(t *testing.T) { _set(t, "parent1", "4", "somestring") },
			func(t *testing.T) { _set(t, "parent2", "4", "some\ttab string odd") },
			func(t *testing.T) { _set(t, "parent3", "4", "some \n string with newline") },
			func(t *testing.T) { _set(t, "parent4", "4", "some 84921 numbered \t odd \n string") },
		},
		"set array and get": {
			func(t *testing.T) { _setStringItems(t, "parent1", "5", []string{"hello", "apple", "orange"}) },
			func(t *testing.T) {
				_setStringItems(t, "parent2", "5", []string{"hello", "apple", "coconuts", "ora4214421nge"})
			},
		},
		"set map and get": {
			func(t *testing.T) { _setMap(t, "parent1", "6", map[string]string{"hello": "world", "apple": "orange"}) },
			func(t *testing.T) {
				_setMap(t, "parent2", "6", map[string]string{"dasddwa": "wordwadld", "dwadwaqqew": "dasdasdwaw"})
			},
		},
	}
	t.Parallel()
	for name, toRun := range toRun2D {
		_toRun := toRun
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, f := range _toRun {
				t.Run("x", f)
			}
		})
	}
}
