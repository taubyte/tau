// Package generic implements the whole resource surface — new, edit, delete,
// query, list — from the tcc DSL. Flags, prompts, tables, validation and
// persistence are all derived from the schema the compiler itself uses, so a
// resource kind (or one of its fields) exists in the CLI because the DSL says
// so, not because a package was written for it.
package generic

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/cli/common/options"
	"github.com/taubyte/tau/tools/tau/i18n"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/urfave/cli/v2"
)

type link struct {
	common.UnimplementedBasic
	group tcc.Group
	form  *tcc.Form
	// repo is set for kinds the DSL backs with a git repository, code for
	// kinds that carry their own code — both detected from the schema's shape.
	repo *tcc.RepoShape
	code bool
}

// New binds the commands of one resource kind. The form comes from the compiled
// -in schema, so an error here is a build-time defect, not a runtime condition.
func New(g tcc.Group) (func() common.Basic, error) {
	form, err := tcc.FormFor(g.Def)
	if err != nil {
		return nil, err
	}
	l := link{group: g, form: form, repo: form.Repo(), code: form.CodeBacked()}
	return func() common.Basic { return l }, nil
}

// shorthands are command aliases the CLI has always accepted and that are pure
// muscle memory; the canonical names come from the DSL.
var shorthands = map[string][]string{"application": {"app"}}

func (l link) Base() (*cli.Command, []common.Option) {
	cmd := &cli.Command{Name: l.group.Name, ArgsUsage: i18n.ArgsUsageName}
	if l.group.Dir != l.group.Name {
		cmd.Aliases = []string{l.group.Dir}
	}
	cmd.Aliases = append(cmd.Aliases, shorthands[l.group.Name]...)
	return common.Base(cmd, options.NameFlagArg0())
}

func (l link) success(verb, name string) {
	printer.Out.SuccessPrintfln("%s %s: %s", verb, l.group.Name, printer.Out.SprintCyan(name))
}
