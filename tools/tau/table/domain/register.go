package domainTable

import (
	"fmt"
	"strings"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/core/services/auth"
)

func Registered(fqdn string, resp auth.DomainRegistration) {
	pterm.Info.Printfln("Be sure to the following entries of `%s` to your DNS zone:", fqdn)
	fmt.Println(GetRegisterTable(resp))
}

func GetRegisterTable(response auth.DomainRegistration) string {
	t := table.NewWriter()
	t.SetColumnConfigs([]table.ColumnConfig{
		{
			Number:   1,
			WidthMax: 40,
		},
	})

	t.AppendHeader(table.Row{"Domain Registration"})
	t.AppendSeparator()
	t.AppendRow(table.Row{"Entry", response.Entry})
	t.AppendSeparator()
	t.AppendRow(table.Row{"Type", response.Type})
	t.AppendSeparator()

	t.SetStyle(table.Style{
		Box: table.StyleBoxDefault,
	})

	t.AppendRow(table.Row{"Value"}, table.RowConfig{AutoMerge: true})
	t.AppendRow(table.Row{""}, table.RowConfig{AutoMerge: true})
	rendering := t.Render()
	rendering += "\n" + response.Token + "\n" + strings.Repeat("-", 49)

	return rendering
}
