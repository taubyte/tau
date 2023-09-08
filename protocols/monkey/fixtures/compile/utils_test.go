package compile_test

import (
	"fmt"
	"io"
	"net/http"

	"github.com/taubyte/tau/libdream"
)

var (
	testProjectId   = "QmegMKBQmDTU9FUGKdhPFn1ZEtwcNaCA2wmyLW8vJn7wZN"
	testFunctionId  = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J51Tv"
	testFunction2Id = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J5456"
	testSmartOpId   = "QmZY4u91d1YALDN2LTbpVtgwW8iT5cK9PE1bHZqX9J5123"
	testLibraryId   = "QmP6qBNyoLeMLiwk8uYZ8xoT4CnDspYntcY4oCkpVG1byt"
	testWebsiteId   = "QmcrzjxwbqERscawQcXW4e5jyNBNoxLsUYatn63E8XPQq2"
)

func callHal(u *libdream.Universe, path string) ([]byte, error) {
	nodePort, err := u.GetPortHttp(u.Substrate().Node())
	if err != nil {
		return nil, err
	}

	host := fmt.Sprintf("hal.computers.com:%d", nodePort)

	ret, err := http.DefaultClient.Get(fmt.Sprintf("http://%s%s", host, path))
	if err != nil {
		return nil, err
	}

	defer ret.Body.Close()

	return io.ReadAll(ret.Body)
}
