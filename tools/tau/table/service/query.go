package serviceTable

import (
	"strings"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
)

func Query(service *structureSpec.Service) {
	prompts.RenderTable([][]string{
		{"ID", service.Id},
		{"Name", service.Name},
		{"Description", service.Description},
		{"Tags", strings.Join(service.Tags, ", ")},
		{"Protocol", service.Protocol},
	})
}
