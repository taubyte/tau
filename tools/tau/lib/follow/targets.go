package follow

import (
	"context"
	"fmt"
	"net"
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
				p = firstString(tcc.Get(doc, []string{"source", "paths"}))
			} else if t, _ := tcc.Get(doc, []string{"trigger", "type"}).(string); !strings.HasPrefix(t, "http") {
				continue // p2p/pubsub functions have no URL
			}
			domains, _ := tcc.Get(doc, []string{"domains"}).([]any)
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
