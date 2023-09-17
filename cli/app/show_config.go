package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/taubyte/tau/config"
)

// displayConfig displays the configuration details for a given process ID (pid)
func displayConfig(pid string, config *config.Node) error {
	// Create a new table writer
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)

	colConfigs := []table.ColumnConfig{
		{Number: 1, Align: text.AlignCenter, AutoMerge: true},
		{Number: 2, Align: text.AlignLeft},
	}
	t.SetColumnConfigs(colConfigs)

	// Append the process ID to the P2PAnnounce addresses
	addrPID := "/p2p/" + pid
	for i := range config.P2PAnnounce {
		config.P2PAnnounce[i] += addrPID
	}

	// Join the P2PAnnounce addresses with newline separator
	announce := strings.Join(config.P2PAnnounce, "\n")

	// Join the Protocols with newline separator
	protocols := strings.Join(config.Protocols, "\n")

	// Join the Peers with newline separator
	peers := strings.Join(config.Peers, "\n")

	// Join the P2PListen addresses with newline separator
	listen := strings.Join(config.P2PListen, "\n")

	// Create a new table writer for domain details
	domt := table.NewWriter()

	// Append rows for Generated and Services/Protocols domains
	domt.AppendRows([]table.Row{
		{"Generated", fmt.Sprintf("Match(`%s`)", config.GeneratedDomain)},
		{"Services/Protocols", fmt.Sprintf("Match(`%s`)", config.ServicesDomain)},
	})
	domt.SetStyle(table.StyleLight)

	// Create a new table writer for port details
	portst := table.NewWriter()

	// Set the style for the port table
	portst.SetStyle(table.StyleLight)

	// Append rows for default ports (https and dns)
	portst.AppendRow(table.Row{"https", 443})
	portst.AppendRow(table.Row{"dns", 53})

	// Append rows for custom ports
	for name, val := range config.Ports {
		portst.AppendRow(table.Row{"p2p/" + name, val})
	}

	// Create a slice to hold the table rows for data
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

	// Append rows for plugin details
	for _, val := range config.Plugins.Plugins {
		data = append(data, table.Row{"Plugins", val, val})
	}

	// Append each row and separator to the main table
	for _, rdata := range data {
		t.AppendRow(rdata)
		t.AppendSeparator()
	}

	t.SetStyle(table.StyleLight)
	t.Render()

	return nil
}
