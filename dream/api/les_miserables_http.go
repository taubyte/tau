package api

import (
	"fmt"

	"github.com/taubyte/tau/dream"
	httpIface "github.com/taubyte/tau/pkg/http"
)

func (srv *multiverseService) lesMiesrablesHttp() {
	srv.rest.GET(&httpIface.RouteDefinition{
		Path: "/les/miserables/{universe}",
		Vars: httpIface.Variables{
			Required: []string{"universe"},
		},
		Handler: srv.apiHandlerLesMiesrable,
	})
}

type EchartNode struct {
	Id       string         `json:"id"`
	Name     string         `json:"name"`
	Category int            `json:"category"`
	Value    map[string]int `json:"value"`
}

type EchartLinks struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type EchartCat struct {
	Name string `json:"name"`
}

type Echart struct {
	Nodes      []*EchartNode  `json:"nodes"`
	Links      []*EchartLinks `json:"links"`
	Categories []*EchartCat   `json:"categories"`
}

func (srv *multiverseService) apiHandlerLesMiesrable(ctx httpIface.Context) (interface{}, error) {
	universeName, err := ctx.GetStringVariable("universe")
	if err != nil {
		return nil, err
	}

	u, err := dream.GetUniverse(universeName)
	if err != nil {
		return nil, fmt.Errorf("universe `%s` does not exit", universeName)
	}

	ret := &Echart{
		Nodes:      make([]*EchartNode, 0),
		Links:      make([]*EchartLinks, 0),
		Categories: make([]*EchartCat, 0),
	}

	for i, n := range u.All() {
		_cat, ok := u.Lookup(n.ID().String())
		if !ok {
			continue
		}

		cat := _cat.Name
		name := fmt.Sprintf("%s@%s", cat, u.Name())
		pid := n.ID().String()
		ret.Categories = append(ret.Categories, &EchartCat{
			Name: cat,
		})

		node, _ := u.Lookup(pid)

		ret.Nodes = append(ret.Nodes, &EchartNode{
			Id:       pid,
			Name:     name,
			Value:    node.Ports,
			Category: i,
		})

		for _, l := range n.Peer().Peerstore().Peers() {
			if l.String() != pid {
				ret.Links = append(ret.Links, &EchartLinks{
					Source: pid,
					Target: l.String(),
				})
			}
		}
	}

	return ret, nil
}
