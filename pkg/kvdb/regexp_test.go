package kvdb

import (
	"context"
	"fmt"
	"testing"

	logging "github.com/ipfs/go-log/v2"
)

var logger = logging.Logger("test.kvdb")

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if b[i] != v {
			return false
		}
	}
	return true
}

func TestAsyncRegExp(t *testing.T) {
	testDB, err := opener{mockPeer(t)}.New(logger, "test", 5)
	if err != nil {
		t.Fatal(err)
	}
	defer testDB.Close()

	testKeys := []string{
		"/Z/n", "/Z/n/1", "/Z/n/2@2",
		"/Z/m", "/Z/m/1/1@3", "/Z/m/1/2",
		"/a", "/a/1", "/a/2",
		"/b", "/b/1/1", "/b/1/2",
		"/c", "/c/99", "/c/11",
	}

	ctx := context.Background()

	for _, k := range testKeys {
		err := testDB.Put(ctx, k, []byte("test"))
		if err != nil {
			t.Error(err)
			return
		}
	}

	res, err := testDB.ListRegEx(ctx, "", "/[ab]/?.*")
	if err != nil {
		t.Error(err)
		return
	}

	correctRes := []string{
		"/a/1",
		"/a/2",
		"/a",
		"/b/1/1",
		"/b/1/2",
		"/b",
	}

	if !equalStringSlices(res, correctRes) {
		fmt.Println(res)
		fmt.Println(correctRes)
		t.Error("Regex list return wrong result")
		return
	}

	prefix_res, err := testDB.ListRegEx(ctx, "/Z/m", "^/Z/[mn](.*)$")
	if err != nil {
		t.Error(err)
		return
	}

	prefix_correctRes := []string{
		"/Z/m/1/1@3",
		"/Z/m/1/2",
	}

	if !equalStringSlices(prefix_res, prefix_correctRes) {
		fmt.Println(prefix_res)
		fmt.Println(prefix_correctRes)
		t.Error("Regex list return wrong result")
		return
	}
}
