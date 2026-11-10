package follow

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/taubyte/tau/tools/tau/clients/dream"
	"github.com/taubyte/tau/tools/tau/common"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/tcc"
)

// Targets lists, for each http function (and website when withWebsites) of the
// project, its URL on every node. Dream: each gateway (else substrate) node's
// HTTP port. Remote: each address the resource's domain resolves to.
func Targets(st *tcc.Store, withWebsites bool) ([]Target, error) {
	profile, err := loginLib.GetSelectedProfile()
	if err != nil {
		return nil, err
	}
	var dreamNodes map[string]string
	if profile.CloudType == common.DreamCloud {
		if dreamNodes, err = dreamHTTPNodes(); err != nil {
			return nil, err
		}
	}

	groups := []string{"functions"}
	if withWebsites {
		groups = append(groups, "websites")
	}
	var out []Target
	for _, group := range groups {
		names, _ := st.List(group)
		for _, name := range names {
			doc, err := st.Doc(group, name)
			if err != nil {
				return nil, err
			}
			p := firstString(tcc.Get(doc, []string{"trigger", "paths"}))
			if group == "websites" {
				p = firstString(tcc.Get(doc, []string{"paths"})) // website yaml keeps paths at top level
				if p == "/" {
					p = firstString(tcc.Get(doc, []string{"source", "paths"}))
				}
			} else if t, _ := tcc.Get(doc, []string{"trigger", "type"}).(string); !strings.HasPrefix(t, "http") {
				continue // p2p/pubsub functions have no URL
			}
			domains := domainsOf(doc)
			if len(domains) == 0 {
				continue
			}
			dname, _ := domains[0].(string)
			ddoc, err := st.Doc("domains", dname)
			if err != nil {
				return nil, err
			}
			fqdn, _ := tcc.Get(ddoc, []string{"fqdn"}).(string)
			if fqdn == "" {
				continue
			}
			resource := strings.TrimSuffix(group, "s") + " " + name
			method, _ := tcc.Get(doc, []string{"trigger", "method"}).(string)
			if dreamNodes != nil {
				for node, base := range dreamNodes {
					out = append(out, Target{Resource: resource, Node: node, URL: base + p, Host: fqdn, Method: strings.ToUpper(method)})
				}
				continue
			}
			addrs, err := net.LookupHost(fqdn)
			if err != nil {
				return nil, fmt.Errorf("resolving %s: %w", fqdn, err)
			}
			for _, a := range addrs {
				out = append(out, Target{Resource: resource, Node: a, URL: "https://" + net.JoinHostPort(a, "443") + p, Host: fqdn, Method: strings.ToUpper(method)})
			}
		}
	}
	return out, nil
}

// domainsOf reads a resource's domains. A function keeps them under trigger;
// websites and older functions at the top level, as pkg/schema/functions reads them.
func domainsOf(doc tcc.Doc) []any {
	if domains, ok := tcc.Get(doc, []string{"trigger", "domains"}).([]any); ok {
		return domains
	}
	domains, _ := tcc.Get(doc, []string{"domains"}).([]any)
	return domains
}

func firstString(v any) string {
	if l, ok := v.([]any); ok && len(l) > 0 {
		if s, ok := l[0].(string); ok {
			return s
		}
	}
	return "/"
}

// dreamHTTPNodes maps each Dream node that serves HTTP to its base URL.
func dreamHTTPNodes() (map[string]string, error) {
	chart, err := dream.Status(context.Background())
	if err != nil {
		return nil, err
	}
	pick := func(kind string) map[string]string {
		out := map[string]string{}
		for _, n := range chart.Nodes {
			if port, ok := n.Value["http"]; ok && strings.Contains(n.Name, kind) {
				out[n.Name] = fmt.Sprintf("http://127.0.0.1:%d", port)
			}
		}
		return out
	}
	if nodes := pick("gateway"); len(nodes) > 0 {
		return nodes, nil
	}
	if nodes := pick("substrate"); len(nodes) > 0 {
		return nodes, nil
	}
	return nil, fmt.Errorf("no gateway or substrate node in this Dream universe")
}

// SourceRepos are the project's website and library repositories, labelled as
// RepoNames does, for Wait to follow the builds a Dream push-all starts for them.
func SourceRepos(st *tcc.Store, since int64) []Repo {
	var out []Repo
	for _, group := range []string{"websites", "libraries"} {
		list, _ := st.List(group)
		for _, n := range list {
			doc, err := st.Doc(group, n)
			if err != nil {
				continue
			}
			if full, _ := tcc.Get(doc, []string{"source", "github", "fullname"}).(string); full != "" {
				out = append(out, Repo{Name: map[string]string{"websites": "website ", "libraries": "library "}[group] + n, Remote: RepoName(full), Since: since})
			}
		}
	}
	return out
}

// RepoNames labels the project's website and library repository ids as tau
// does ("website x", "library x"); config and code are named from the repo.
func RepoNames(st *tcc.Store) map[int]string {
	names := map[int]string{}
	for _, group := range []string{"websites", "libraries"} {
		list, _ := st.List(group)
		for _, n := range list {
			doc, err := st.Doc(group, n)
			if err != nil {
				continue
			}
			id, _ := tcc.Get(doc, []string{"source", "github", "id"}).(string)
			if i, err := strconv.Atoi(id); err == nil {
				names[i] = map[string]string{"websites": "website ", "libraries": "library "}[group] + n
			}
		}
	}
	return names
}
