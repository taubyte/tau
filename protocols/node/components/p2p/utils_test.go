package p2p

import (
	"context"
	"errors"
	"fmt"

	"bitbucket.org/taubyte/go-node-tvm/cache"
	"bitbucket.org/taubyte/vm-test-examples/structure"
	p2p "github.com/taubyte/go-interfaces/p2p/peer"
	"github.com/taubyte/go-interfaces/services/tns"
	structureSpec "github.com/taubyte/go-specs/structure"
)

var (
	testProject    = "Qmc3WjpDvCaVY3jWmxranUY7roFhRj66SNqstiRbKxDbU4"
	testCommit     = "qwertyuiop"
	testService    = "someService"
	testServiceId  = "someServiceId"
	testFunction   = "someFunction"
	testFunctionId = "someFunctionId"
)

func fakeFetch(services map[string]structureSpec.Service, functions map[string]structureSpec.Function) {
	structure.FakeFetchMethod = func(path tns.Path) (tns.Object, error) {
		if path.String() == fmt.Sprintf("projects/%s/branches/master/current", testProject) {
			return structure.ResponseObject{Object: testCommit}, nil
		}

		if path.Slice()[6] == "services" {
			return structure.ResponseObject{Object: services}, nil
		} else if path.Slice()[6] == "functions" {
			return structure.ResponseObject{Object: functions}, nil
		}

		return nil, errors.New("Nothing found here")
	}
}

func NewTestService(node p2p.Node) *Service {
	nodeService := structure.MockNodeService(node, context.Background())

	return &Service{
		Service: nodeService,
		cache:   cache.New(),
	}
}
