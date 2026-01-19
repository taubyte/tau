package pass4

import (
	"fmt"
	"slices"

	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	specs "github.com/taubyte/tau/pkg/specs/website"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type websites struct {
	branch string
}

func Websites(branch string) transform.Transformer[object.Refrence] {
	return &websites{branch: branch}
}

func (w *websites) Process(ct transform.Context[object.Refrence], config object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	if len(ct.Path()) < 2 {
		return nil, fmt.Errorf("path %v is too short", ct.Path())
	}

	root, ok := ct.Path()[0].(object.Object[object.Refrence])
	if !ok {
		return nil, fmt.Errorf("root is not an object")
	}

	configRoot, ok := ct.Path()[1].(object.Object[object.Refrence])
	if !ok {
		return nil, fmt.Errorf("config root is not an object")
	}

	appId := ""
	if configRoot != config {
		appsObj, err := configRoot.Child("applications").Object()
		if err != nil {
			return nil, fmt.Errorf("fetching applications failed with %w", err)
		}
		appId = appsObj.Child(config).Name()
	}

	websiteConfig, err := config.Child(string(specs.PathVariable)).Object()
	if err == object.ErrNotExist {
		return config, nil
	} else if err != nil {
		return nil, fmt.Errorf("fetching website config failed with %w", err)
	}

	projectId, err := configRoot.GetString("id")
	if err != nil {
		return nil, fmt.Errorf("project id is not a string: %w", err)
	}

	index, err := root.CreatePath("indexes")
	if err != nil {
		return nil, fmt.Errorf("creating path for indexes failed with %w", err)
	}

	for _, websiteId := range websiteConfig.Children() {
		tnsPath, err := specs.Tns().IndexValue(w.branch, projectId, appId, websiteId)
		if err != nil {
			return nil, fmt.Errorf("getting index value for website %s failed with %w", websiteId, err)
		}

		websiteObj, err := websiteConfig.Child(websiteId).Object()
		if err != nil {
			return nil, fmt.Errorf("fetching website object for %s failed with %w", websiteId, err)
		}

		gitProvider, err := websiteObj.GetString("provider")
		if err != nil {
			return nil, fmt.Errorf("git provider is not a string: %w", err)
		}

		githubId, err := websiteObj.GetString("repository-id")
		if err != nil {
			return nil, fmt.Errorf("git repository is not a string: %w", err)
		}

		repoPath, err := methods.GetRepositoryPath(gitProvider, githubId, projectId)
		if err != nil {
			return nil, fmt.Errorf("getting repository path for %s failed with %w", githubId, err)
		}

		// set repository path
		index.Set(repoPath.Type().String(), common.WebsiteRepository)
		index.Set(repoPath.Resource(websiteId).String(), tnsPath.String())

		// referencing domains

		secondaryDomains, _ := configRoot.Child("domains").Object() // equals global domains when in an app
		primaryDomains, _ := config.Child("domains").Object()       // equals apps domains when in an app
		if primaryDomains == nil {                                  // if app has no domains, use global domains
			primaryDomains = secondaryDomains
		}
		if primaryDomains == secondaryDomains { // if we're in project-level, use global domains
			secondaryDomains = nil
		}

		domainsVal := websiteObj.Get("domains")
		domains, ok := domainsVal.([]string)
		if !ok && domainsVal != nil {
			return nil, fmt.Errorf("domains is not a []string")
		}
		if domains == nil {
			domains = []string{}
		}
		for _, domainId := range domains {
			domainObj, err := primaryDomains.Child(domainId).Object()
			if err != nil {
				if secondaryDomains != nil {
					domainObj, err = secondaryDomains.Child(domainId).Object()
					if err != nil {
						return nil, fmt.Errorf("fetching domain object for %s failed with %w", domainId, err)
					}
				} else {
					return nil, fmt.Errorf("fetching domain object for %s failed with %w", domainId, err)
				}
			}

			fqdnStr, err := domainObj.GetString("fqdn")
			if err != nil {
				return nil, fmt.Errorf("fqdn is not a string for domain %s: %w", domainId, err)
			}
			domainPath, err := specs.Tns().HttpPath(fqdnStr)
			if err != nil {
				return nil, fmt.Errorf("getting HTTP path for domain %s failed with %w", domainId, err)
			}

			domainLinkPath := domainPath.Versioning().Links().String()
			links, ok := index.Get(domainLinkPath).([]string)
			if !ok {
				links = []string{}
			}

			if !slices.Contains(links, tnsPath.String()) {
				links = append(links, tnsPath.String())
			}

			index.Set(domainLinkPath, links)
		}
	}

	return config, nil
}
