package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/taubyte/tau/pkg/config"
)

func displayConfig(pid string, cfg config.Config) error {
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)

	colConfigs := []table.ColumnConfig{
		{Number: 1, Align: text.AlignCenter, AutoMerge: true},
		{Number: 2, Align: text.AlignLeft},
	}
	t.SetColumnConfigs(colConfigs)

	addrPID := "/p2p/" + pid
	announceSlice := cfg.P2PAnnounce()
	announceWithPID := make([]string, len(announceSlice))
	for i, a := range announceSlice {
		announceWithPID[i] = a + addrPID
	}
	announce := strings.Join(announceWithPID, "\n")

	services := strings.Join(cfg.Services(), "\n")
	peers := strings.Join(cfg.Peers(), "\n")
	listen := strings.Join(cfg.P2PListen(), "\n")

	domt := table.NewWriter()
	domt.AppendRows([]table.Row{
		{"Generated", cfg.GeneratedDomain()},
		{"Aliases", fmt.Sprint(cfg.AliasDomains())},
	})
	domt.SetStyle(table.StyleLight)

	portst := table.NewWriter()
	portst.SetStyle(table.StyleLight)
	portst.AppendRow(table.Row{"https", 443})
	portst.AppendRow(table.Row{"dns", 53})
	for name, val := range cfg.Ports() {
		portst.AppendRow(table.Row{"p2p/" + name, val})
	}

	loc := cfg.Location()
	locStr := ""
	if loc != nil {
		locStr = fmt.Sprintf("%f,%f", loc.Latitude, loc.Longitude)
	}
	data := []table.Row{
		{"ID", pid},
		{"Location", locStr},
		{"Root", cfg.Root()},
		{"Shape", cfg.Shape()},
		{"Network", cfg.NetworkFqdn()},
		{"Domain", domt.Render()},
		{"Services", services},
		{"Peers", peers},
		{"P2PListen", listen},
		{"P2PAnnounce", announce},
		{"Ports", portst.Render()},
	}

	for _, val := range cfg.Plugins().Plugins {
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
