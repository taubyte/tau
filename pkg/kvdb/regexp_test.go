package kvdb

import (
	"context"
	"fmt"
	"testing"

	"github.com/taubyte/tau/core/kvdb"
	keypair "github.com/taubyte/tau/p2p/keypair"

	peer "github.com/taubyte/tau/p2p/peer"

	logging "github.com/ipfs/go-log/v2"
)

var logger = logging.Logger("test.kvdb")

var testDB kvdb.KVDB

func init() {
	ctx := context.Background()

	node, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11001)},
		nil,
		true,
		false,
	)
	if err != nil {
		panic(fmt.Sprintf("Peer creation returned error `%s`", err.Error()))
	}

	factory := New(node)
	testDB, err = factory.New(logger, "test", 5)
	if err != nil {
		panic(err)
	}
}

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

	c, err := testDB.ListRegExAsync(ctx, "", "/[ab]/?.*")
	if err != nil {
		t.Error(err)
		return
	}

	res := make([]string, 0)
	for i := range c {
		res = append(res, i)
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

	c, err = testDB.ListRegExAsync(ctx, "/Z/m", "^/Z/[mn](.*)$")
	if err != nil {
		t.Error(err)
		return
	}

	prefix_res := make([]string, 0)
	for i := range c {
		prefix_res = append(prefix_res, i)
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
