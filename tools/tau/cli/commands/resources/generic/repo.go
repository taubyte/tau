package generic

import (
	"fmt"
	"strings"

	httpClient "github.com/taubyte/tau/clients/http/auth"
	repositoryCommands "github.com/taubyte/tau/tools/tau/cli/commands/resources/repository"
	authClient "github.com/taubyte/tau/tools/tau/clients/auth_client"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/taubyte/tau/tools/tau/templates"
	"github.com/taubyte/tau/utils/id"
	"github.com/urfave/cli/v2"
)

// resource adapts a tcc document to the repository commands' view of it.
type resource struct {
	l     link
	st    *tcc.Store
	shape *tcc.RepoShape
	name  string
	doc   tcc.Doc
}

func (r *resource) Get() repositoryCommands.Getter { return getter{r} }
func (r *resource) Set() repositoryCommands.Setter { return setter{r} }

type getter struct{ r *resource }
type setter struct{ r *resource }

func (g getter) Name() string { return g.r.name }
func (g getter) Description() string {
	s, _ := tcc.Get(g.r.doc, []string{"description"}).(string)
	return s
}
func (g getter) RepoName() string {
	s, _ := tcc.Get(g.r.doc, g.r.shape.Under(g.r.doc, g.r.shape.Fullname)).(string)
	return s
}
func (g getter) RepoID() string {
	s, _ := tcc.Get(g.r.doc, g.r.shape.Under(g.r.doc, g.r.shape.ID)).(string)
	return s
}
func (g getter) Branch() string {
	s, _ := tcc.Get(g.r.doc, g.r.shape.Branch).(string)
	return s
}
func (g getter) RepositoryURL() string {
	return repositoryLib.GetRepositoryUrl(tcc.ActiveBranch(g.r.doc, g.r.shape.Provider), g.RepoName())
}

func (s setter) RepoID(id string) {
	tcc.Set(s.r.doc, s.r.shape.Under(s.r.doc, s.r.shape.ID), id)
}
func (s setter) RepoName(name string) {
	tcc.Set(s.r.doc, s.r.shape.Under(s.r.doc, s.r.shape.Fullname), name)
}

// commands wires this resource kind into the shared repository command driver.
func (l link) commands() repositoryCommands.Commands {
	return repositoryCommands.InitCommand(&repositoryCommands.LibCommands{
		Type: l.group.Name,

		PromptNew:         l.promptNewRepo,
		LibNew:            write,
		I18nCreated:       func(name string) { l.success("Created", name) },
		PromptsCreateThis: "Create this " + l.group.Name + "?",

		PromptsGetOrSelect: l.selectRepo,
		PromptsEdit:        l.promptEditRepo,
		I18nEdited:         func(name string) { l.success("Edited", name) },
		PromptsEditThis:    "Edit this " + l.group.Name + "?",

		I18nCheckedOut: func(url, branch string) {
			printer.Out.SuccessPrintfln("Checked out %s branch %s", url, branch)
		},
		I18nPulled: func(url string) { printer.Out.SuccessPrintfln("Pulled %s", url) },
		I18nPushed: func(url, msg string) { printer.Out.SuccessPrintfln("Pushed %s: %s", url, msg) },

		LibSet:         write,
		TableConfirm:   l.confirmRepo,
		I18nRegistered: func(url string) { printer.Out.SuccessPrintfln("Registered %s", url) },
	})
}

func write(res repositoryCommands.Resource) error {
	r := res.(*resource)
	return r.st.Write(r.l.group.Dir, r.name, r.doc)
}

func (l link) confirmRepo(ctx *cli.Context, res repositoryCommands.Resource, prompt string) bool {
	r := res.(*resource)
	return l.confirm(ctx, prompt, r.name, r.doc)
}

func (l link) selectRepo(ctx *cli.Context) (repositoryCommands.Resource, error) {
	st, err := open()
	if err != nil {
		return nil, err
	}
	name, doc, err := st.Select(ctx, l.group)
	if err != nil {
		return nil, err
	}
	return &resource{l: l, st: st, shape: l.repo, name: name, doc: doc}, nil
}

func (l link) promptNewRepo(ctx *cli.Context) (any, repositoryCommands.Resource, error) {
	st, err := open()
	if err != nil {
		return nil, nil, err
	}
	taken, err := st.List(l.group.Dir)
	if err != nil {
		return nil, nil, err
	}
	name, err := prompts.GetOrRequireAUniqueName(ctx, l.group.Name+" Name:", taken)
	if err != nil {
		return nil, nil, err
	}
	projectID, err := st.ProjectID()
	if err != nil {
		return nil, nil, err
	}

	r := &resource{l: l, st: st, shape: l.repo, name: name, doc: tcc.Doc{"id": id.Generate(projectID, name)}}
	if err := l.fill(ctx, st, name, r.doc); err != nil {
		return nil, nil, err
	}
	info, err := l.repositoryInfo(ctx, r, true)
	return info, r, err
}

func (l link) promptEditRepo(ctx *cli.Context, res repositoryCommands.Resource) (any, error) {
	r := res.(*resource)
	if err := l.fill(ctx, r.st, r.name, r.doc); err != nil {
		return nil, err
	}
	return l.repositoryInfo(ctx, r, false)
}

// repositoryInfo either generates a new repository from a template or attaches
// an existing one, mirroring what the repository driver expects back.
func (l link) repositoryInfo(ctx *cli.Context, r *resource, isNew bool) (any, error) {
	if isNew && prompts.GetGenerateRepository(ctx) {
		return l.generateRepository(ctx, r)
	}

	selected, err := prompts.SelectARepository(ctx, &repositoryLib.Info{
		Type:     l.group.Name,
		FullName: r.Get().RepoName(),
		ID:       r.Get().RepoID(),
	})
	if err != nil {
		return nil, err
	}
	r.Set().RepoID(selected.ID)
	r.Set().RepoName(selected.FullName)

	projectConfig, err := projectLib.SelectedProjectConfig()
	if err != nil {
		return nil, err
	}
	if !selected.HasBeenCloned(projectConfig, tcc.ActiveBranch(r.doc, r.shape.Provider)) {
		selected.DoClone = prompts.GetClone(ctx)
	}
	return selected, nil
}

func (l link) generateRepository(ctx *cli.Context, r *resource) (*repositoryLib.InfoTemplate, error) {
	client, err := authClient.Load()
	if err != nil {
		return nil, err
	}

	name := fmt.Sprintf("tb_%s_%s", l.group.Name, r.name)
	if ctx.IsSet(flags.RepositoryName.Name) {
		name = ctx.String(flags.RepositoryName.Name)
	}
	for {
		taken, err := repositoryNameTaken(client, name)
		if err != nil {
			return nil, err
		}
		if !taken {
			break
		}
		printer.Out.WarningPrintfln("Repository name %s is already taken", name)
		if name, err = prompts.GetOrRequireARepositoryName(ctx); err != nil {
			return nil, err
		}
	}

	templateMap, err := templates.Get().RepositoryTemplates(l.group.Dir)
	if err != nil {
		return nil, err
	}
	url, err := prompts.SelectATemplate(ctx, templateMap)
	if err != nil {
		return nil, err
	}

	return &repositoryLib.InfoTemplate{
		RepositoryName: name,
		Info:           templates.TemplateInfo{URL: url},
		Private:        prompts.GetPrivate(ctx),
	}, nil
}

func repositoryNameTaken(client *httpClient.Client, name string) (bool, error) {
	fullName := name
	if !strings.Contains(name, "/") {
		user, err := client.User().Get()
		if err != nil {
			return false, err
		}
		fullName = user.Login + "/" + name
	}
	// A successful lookup means the name is in use.
	_, err := client.GetRepositoryByName(fullName)
	return err == nil, nil
}
