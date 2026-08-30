package schema_test

import (
	"fmt"
	"sort"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

func TestZZPopulation(t *testing.T) {
	marked, total := 0, 0
	byKind := map[string][]string{}
	for _, g := range schema.V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		kind, _ := g.Match.(string)
		var walk func(n *engine.Node)
		walk = func(n *engine.Node) {
			for _, a := range n.Attributes {
				total++
				if a.Meta["perEnv"] == true {
					marked++
					byKind[kind] = append(byKind[kind], a.Name)
				}
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(g.Children[0])
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		sort.Strings(byKind[k])
		fmt.Printf("%-12s %v\n", k, byKind[k])
	}
	fmt.Printf("\nMARKED %d of %d attributes\n", marked, total)
}
