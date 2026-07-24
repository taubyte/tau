package generic

import (
	"strings"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/urfave/cli/v2"
)

// repoFlags are the extra inputs of the repository flow: which repo to attach or
// generate, and whether to clone it locally.
var repoFlags = []cli.Flag{
	flags.Template,
	flags.RepositoryName,
	flags.RepositoryId,
	flags.Clone,
	flags.EmbedToken,
	flags.GenerateRepo,
	flags.Private,
}

// aliases keeps the CLI's established shorthands (-b, -p, -t, ...) for DSL
// fields whose flag name matches one of the shared flags.
var aliases = map[string][]string{}

func init() {
	for _, f := range []cli.Flag{
		flags.Branch, flags.Paths, flags.Description, flags.Tags,
		flags.Timeout, flags.Memory, flags.Call,
	} {
		names := f.Names()
		aliases[names[0]] = names[1:]
	}
}

// editable reports whether a field is one the user answers directly: the id is
// derived, the name is the command's argument, and for a repository-backed kind
// the repository block is owned by the repository flow (select or generate)
// rather than typed in field by field.
func (l link) editable(f tcc.Field) bool {
	if f.Widget == tcc.WidgetCID || f.Key == "name" {
		return false
	}
	// The repository itself is chosen by the repository flow (select or
	// generate), not typed in field by field; its provider still is.
	if l.repo != nil && len(f.Path) == 3 && f.Path[0] == "source" {
		return false
	}
	return true
}

// flagsFor turns a resource's fields into CLI flags. A list field takes a
// repeatable/comma-separated string slice, a boolean a switch, everything else a
// string; enums advertise their members in the usage line.
func (l link) flagsFor() []cli.Flag {
	var out []cli.Flag
	for _, f := range l.form.Fields {
		if !l.editable(f) {
			continue
		}
		usage := f.Description
		switch {
		case len(f.Enum) > 0:
			usage += " One of: " + strings.Join(f.Enum, ", ") + "."
		case f.IsSelector:
			usage += " One of: " + strings.Join(f.Alternatives, ", ") + "."
		}
		switch f.Widget {
		case tcc.WidgetList, tcc.WidgetRefList:
			out = append(out, &cli.StringSliceFlag{Name: f.Flag, Aliases: aliases[f.Flag], Usage: usage})
		case tcc.WidgetSwitch:
			out = append(out, &cli.BoolFlag{Name: f.Flag, Aliases: aliases[f.Flag], Usage: usage})
		default:
			out = append(out, &cli.StringFlag{Name: f.Flag, Aliases: aliases[f.Flag], Usage: usage})
		}
	}
	if l.repo != nil {
		out = append(out, repoFlags...)
	}
	if l.code {
		out = append(out, codeFlags...)
	}
	return out
}
