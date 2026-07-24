package generic

import (
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/taubyte/tau/utils/id"
	"github.com/urfave/cli/v2"
)

// A repository-backed kind routes new/edit through the shared repository driver
// (it has to create or attach a repo and optionally clone it) and gains the
// repository verbs; every other kind uses the plain document flow.

func (l link) New() common.Command {
	action := l.new
	if l.repo != nil {
		action = l.commands().New
	}
	return common.Create(&cli.Command{Flags: l.flagsFor(), Action: action})
}

func (l link) Edit() common.Command {
	action := l.edit
	if l.repo != nil {
		action = l.commands().Edit
	}
	return common.Create(&cli.Command{Flags: l.flagsFor(), Action: action})
}

func (l link) Clone() common.Command {
	if l.repo == nil {
		return common.NotImplemented
	}
	return l.commands().CloneCmd()
}

func (l link) Push() common.Command {
	if l.repo == nil {
		return common.NotImplemented
	}
	return l.commands().PushCmd()
}

func (l link) Pull() common.Command {
	if l.repo == nil {
		return common.NotImplemented
	}
	return l.commands().PullCmd()
}

func (l link) Checkout() common.Command {
	if l.repo == nil {
		return common.NotImplemented
	}
	return l.commands().CheckoutCmd()
}

func (l link) Import() common.Command {
	if l.repo == nil {
		return common.NotImplemented
	}
	return common.Create(&cli.Command{Action: l.commands().Import})
}

func (l link) Delete() common.Command {
	return common.Create(&cli.Command{Action: l.delete})
}

func (l link) Query() common.Command {
	return common.Create(&cli.Command{Flags: []cli.Flag{flags.List}, Action: l.query})
}

func (l link) List() common.Command {
	return common.Create(&cli.Command{Action: l.list})
}

// open confirms a project is selected and opens the tcc session over its config.
func open() (*tcc.Store, error) {
	if err := projectLib.ConfirmSelectedProject(); err != nil {
		return nil, err
	}
	return tcc.Open()
}

func (l link) new(ctx *cli.Context) error {
	st, err := open()
	if err != nil {
		return err
	}

	taken, err := st.List(l.group.Dir)
	if err != nil {
		return err
	}
	name, err := prompts.GetOrRequireAUniqueName(ctx, l.group.Name+" Name:", taken)
	if err != nil {
		return err
	}

	projectID, err := st.ProjectID()
	if err != nil {
		return err
	}

	doc := tcc.Doc{"id": id.Generate(projectID, name)}
	var templateURL string
	if l.code {
		if templateURL, err = l.seedFromTemplate(ctx, doc); err != nil {
			return err
		}
	}
	if err := l.fill(ctx, st, name, doc); err != nil {
		return err
	}

	if !l.confirm(ctx, "Create this "+l.group.Name+"?", name, doc) {
		return nil
	}
	if err := st.Write(l.group.Dir, name, doc); err != nil {
		return err
	}
	if err := l.scaffold(name, templateURL); err != nil {
		return err
	}
	l.success("Created", name)
	// A container is a scope; creating one puts you in it.
	if l.group.Container {
		return l.enterScope(name)
	}
	return nil
}

func (l link) edit(ctx *cli.Context) error {
	st, err := open()
	if err != nil {
		return err
	}
	name, doc, err := st.Select(ctx, l.group)
	if err != nil {
		return err
	}
	if err := l.fill(ctx, st, name, doc); err != nil {
		return err
	}
	if !l.confirm(ctx, "Edit this "+l.group.Name+"?", name, doc) {
		return nil
	}
	if err := st.Write(l.group.Dir, name, doc); err != nil {
		return err
	}
	l.success("Edited", name)
	return nil
}

func (l link) delete(ctx *cli.Context) error {
	st, err := open()
	if err != nil {
		return err
	}
	name, doc, err := st.Select(ctx, l.group)
	if err != nil {
		return err
	}
	if !l.confirm(ctx, "Delete this "+l.group.Name+"?", name, doc) {
		return nil
	}
	if err := st.Delete(l.group.Dir, name); err != nil {
		return err
	}
	if l.code {
		if err := l.removeCode(name); err != nil {
			return err
		}
	}
	if l.group.Container && st.Application() == name {
		if err := l.clearScope(); err != nil {
			return err
		}
	}
	l.success("Deleted", name)
	return nil
}

func (l link) query(ctx *cli.Context) error {
	if ctx.Bool(flags.List.Name) {
		return l.list(ctx)
	}
	st, err := open()
	if err != nil {
		return err
	}
	name, doc, err := st.Select(ctx, l.group)
	if err != nil {
		return err
	}
	prompts.RenderTable(l.rows(name, doc, true))
	return nil
}

func (l link) list(ctx *cli.Context) error {
	st, err := open()
	if err != nil {
		return err
	}
	names, err := st.List(l.group.Dir)
	if err != nil {
		return err
	}
	docs := make([]tcc.Doc, len(names))
	for i, name := range names {
		if docs[i], err = st.Doc(l.group.Dir, name); err != nil {
			return err
		}
	}
	l.listTable(names, docs)
	return nil
}
