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
	// EVERY FIELD OF THE SOURCE SWITCH IS TYPED, including the repository's.
	//
	// It used to hide them, because in v1 a website WAS a repository and the
	// repository flow owned the block. In v2 a repository is one branch of a
	// source switch an author picks between, so hiding the branch's fields
	// would leave no way to write one — and the flow that would have filled
	// them in is a GitHub picker a self-hosted project may never use.
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
	// A KIND CAN BE BOTH, and both sets offer --template. A repository-backed
	// kind that is also code-backed — every workload whose source may be
	// another repository — would otherwise register the same flag twice, which
	// urfave/cli panics on at command construction.
	if l.repo != nil {
		out = appendFlags(out, repoFlags...)
	}
	if l.code {
		out = appendFlags(out, codeFlags...)
	}
	return out
}

// appendFlags adds what is not already there, by name.
func appendFlags(out []cli.Flag, add ...cli.Flag) []cli.Flag {
	taken := map[string]bool{}
	for _, f := range out {
		for _, n := range f.Names() {
			taken[n] = true
		}
	}

	for _, f := range add {
		if taken[f.Names()[0]] {
			continue
		}
		out = append(out, f)
	}
	return out
}
