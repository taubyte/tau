package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/taubyte/tau/config"
)

func displayConfig(pid string, config *config.Node) error {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)

	colConfigs := []table.ColumnConfig{
		{Number: 1, Align: text.AlignCenter, AutoMerge: true},
		{Number: 2, Align: text.AlignLeft},
	}
	t.SetColumnConfigs(colConfigs)

	addrPID := "/p2p/" + pid
	for i := range config.P2PAnnounce {
		config.P2PAnnounce[i] += addrPID
	}

	announce := strings.Join(config.P2PAnnounce, "\n")

	protocols := strings.Join(config.Protocols, "\n")

	peers := strings.Join(config.Peers, "\n")

	listen := strings.Join(config.P2PListen, "\n")

	domt := table.NewWriter()
	domt.AppendRows([]table.Row{
		{"Generated", fmt.Sprintf("Match(`%s`)", config.GeneratedDomain)},
		{"Services/Protocols", fmt.Sprintf("Match(`%s`)", config.ServicesDomain)},
	})
	domt.SetStyle(table.StyleLight)

	portst := table.NewWriter()
	portst.SetStyle(table.StyleLight)
	portst.AppendRow(table.Row{"https", 443})
	portst.AppendRow(table.Row{"dns", 53})
	for name, val := range config.Ports {
		portst.AppendRow(table.Row{"p2p/" + name, val})
	}

	data := []table.Row{
		{"ID", pid},
		{"Location", fmt.Sprintf("%f,%f", config.Location.Latitude, config.Location.Longitude)},
		{"Root", config.Root},
		{"Shape", config.Shape},
		{"Network", config.NetworkFqdn},
		{"Domain", domt.Render()},
		{"Protocols", protocols},
		{"Peers", peers},
		{"P2PListen", listen},
		{"P2PAnnounce", announce},
		{"Ports", portst.Render()},
	}

	for _, val := range config.Plugins.Plugins {
		data = append(data, table.Row{"Plugins", val, val})
	}

	for _, rdata := range data {
		t.AppendRow(rdata)
		t.AppendSeparator()
	}

	t.SetStyle(table.StyleLight)
	t.Render()

	return nil
}
