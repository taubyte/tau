package buildsTable

import (
	"errors"
	"testing"
)

func TestRepoNamesLooksUpOnce(t *testing.T) {
	calls := 0
	lookup := func(id string) (string, error) {
		calls++
		if id == "bad" {
			return "", errors.New("no")
		}
		return "tb_" + id, nil
	}

	names := make(repoNames)
	for _, id := range []string{"1", "2", "1", "1", "2"} {
		name, err := names.get(id, lookup)
		if err != nil || name != "tb_"+id {
			t.Fatalf("%s: %q %v", id, name, err)
		}
	}
	if calls != 2 {
		t.Fatalf("%d lookups", calls)
	}

	// a failure is not remembered
	for range 2 {
		if _, err := names.get("bad", lookup); err == nil {
			t.Fatal("no error")
		}
	}
	if calls != 4 {
		t.Fatalf("%d lookups", calls)
	}
}
