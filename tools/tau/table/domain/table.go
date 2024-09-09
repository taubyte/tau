package domainTable

import (
	"strings"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

func getTableData(domain *structureSpec.Domain, showId bool) (toRender [][]string) {
	if showId {
		toRender = [][]string{
			{"ID", domain.Id},
		}
	}

	toRender = append(toRender, [][]string{
		{"Name", domain.Name},
		{"Description", domain.Description},
		{"Tags", strings.Join(domain.Tags, ", ")},
		{"FQDN", domain.Fqdn},
		{"Cert-Type", domain.CertType},
	}...)

	if domain.CertType != "auto" {
		toRender = append(toRender, [][]string{
			{"Cert-File", domain.CertFile},
			{"Key-File", domain.KeyFile},
		}...)
	}

	return toRender
}
