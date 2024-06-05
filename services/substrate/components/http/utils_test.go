package http

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/core/services/tns"
	functionSpec "github.com/taubyte/tau/pkg/specs/function"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	websiteSpec "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/services/substrate/components/http/common"
	"github.com/taubyte/tau/services/substrate/components/structure"
	"github.com/taubyte/tau/services/substrate/runtime/cache"
	slices "github.com/taubyte/utils/slices/string"
)

var (
	websites    map[string]structureSpec.Website
	functions   map[string]structureSpec.Function
	domains     map[string]structureSpec.Domain
	testProject = "Qmc3WjpDvCaVY3jWmxranUY7roFhRj66SNqstiRbKxDbU4"
	testCommit  = "qwertyuiop"
	testFileId  = "asdfghjkl"
)

type specialPath struct {
	path     tns.Path
	website  *structureSpec.Website
	function *structureSpec.Function
}

func fakeFetch(client tns.Client, websites map[string]structureSpec.Website, functions map[string]structureSpec.Function, domains map[string]structureSpec.Domain) error {
	specialTnsPaths := make([]specialPath, 0)
	for _, f := range functions {
		fKey, err := functionSpec.Tns().BasicPath("master", testCommit, testProject, "", f.Id)
		if err != nil {
			return err
		}
		specialTnsPaths = append(specialTnsPaths, specialPath{path: fKey, function: &f})
	}
	for _, w := range websites {
		wKey, err := websiteSpec.Tns().BasicPath("master", testCommit, testProject, "", w.Id)
		if err != nil {
			return err
		}
		specialTnsPaths = append(specialTnsPaths, specialPath{path: wKey, website: &w})
	}
	verifyDomain := func(domainName, fqdn string) bool {
		for _, domain := range domains {
			if domain.Name == domainName && domain.Fqdn == fqdn {
				return true
			}
		}

		return false
	}
	structure.FakeCurrentMethod = func(r tns.Object, branch string) ([]tns.Path, error) {

		tnsPaths := make([]tns.Path, 0)
		slice := r.Path().Slice()
		fqdn := strings.Join(slices.ReverseArray(slice[2:len(slice)-1]), ".")

		if slice[1] == "websites" {
			for _, specialPath := range specialTnsPaths {
				if specialPath.website != nil {
					for _, domain := range specialPath.website.Domains {
						if verifyDomain(domain, fqdn) {
							tnsPaths = append(tnsPaths, specialPath.path)
						}
					}
				}
			}
		}
		if slice[1] == "functions" {
			for _, specialPath := range specialTnsPaths {
				if specialPath.function != nil {
					for _, domain := range specialPath.function.Domains {
						if verifyDomain(domain, fqdn) {
							tnsPaths = append(tnsPaths, specialPath.path)
						}
					}
				}
			}
		}
		return tnsPaths, nil
	}

	structure.FakeFetchMethod = func(path tns.Path) (tns.Object, error) {
		if path.String() == fmt.Sprintf("projects/%s/branches/master/current", testProject) {
			return &structure.ResponseObject{Object: testCommit, Tns: client, InnerPath: path}, nil
		}

		if path.String() == "assets/QmRmbjEmTPFGiEkA75HENjPYDn2biW7UPnWZoDUfhTfqVF" {
			return &structure.ResponseObject{Object: testFileId, Tns: client, InnerPath: path}, nil
		}

		if strings.HasSuffix(path.String(), "/links") {
			tnsPaths := make([]tns.Path, 0)
			slice := path.Slice()
			fqdn := strings.Join(slices.ReverseArray(slice[2:len(slice)-1]), ".")
			for _, specialPath := range specialTnsPaths {
				if specialPath.website != nil {
					for _, domain := range specialPath.website.Domains {
						if verifyDomain(domain, fqdn) {
							tnsPaths = append(tnsPaths, specialPath.path)
						}
					}
				}

				if specialPath.function != nil {
					for _, domain := range specialPath.function.Domains {
						if verifyDomain(domain, fqdn) {
							tnsPaths = append(tnsPaths, specialPath.path)
						}
					}
				}
			}

			return &structure.ResponseObject{Object: tnsPaths, Tns: client, InnerPath: path}, nil
		}

		if len(path.Slice()) > 6 {
			if path.Slice()[6] == "websites" {
				return &structure.ResponseObject{Object: websites[path.Slice()[7]], Tns: client, InnerPath: path}, nil
			} else if path.Slice()[6] == "functions" {
				return &structure.ResponseObject{Object: functions[path.Slice()[7]], Tns: client, InnerPath: path}, nil
			}
		}

		return nil, errors.New("Nothing found here")
	}

	return nil
}

func NewTestService(node peer.Node) *Service {
	nodeService := structure.MockNodeService(node, context.Background())
	return &Service{
		Service: nodeService,
		cache:   cache.New(),
		config:  &config.Node{},
	}
}

var (
	successDomain = "hal.computers.com"
	failDomain    = "ting.computers.com"
	getRequest    = "GET"
)

var (
	functionMatch1   = common.New(successDomain, "/ping", getRequest)
	functionMatch2   = common.New(successDomain, "/ping2", getRequest)
	websiteMatch     = common.New(successDomain, "/", getRequest)
	functionNoMatch1 = common.New(failDomain, "/ping", getRequest)
	functionNoMatch2 = common.New(successDomain, "/ping3", getRequest)
	functionNoMatch3 = common.New(successDomain, "/ping3", getRequest)
	functionNoMatch4 = common.New(successDomain, "/ping", "POST")
)
