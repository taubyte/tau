package status

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/dream/api"
)

func TestRenderNodes(t *testing.T) {
	var out bytes.Buffer
	renderNodes(&out, "blackhole", api.Echart{Nodes: []*api.EchartNode{
		{Name: "tns@blackhole", Value: map[string]int{"p2p": 2, "http": 1}},
		{Name: "client@blackhole", Value: map[string]int{"p2p": 3, "weird": 9}},
	}})
	s := out.String()
	if !strings.HasPrefix(s, "Universe blackhole: 2 nodes") || strings.Index(s, "client@") > strings.Index(s, "tns@") {
		t.Fatal(s)
	}
	for _, want := range []string{"NODE", "HTTP", "P2P", "WEIRD"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in\n%s", want, s)
		}
	}
}
