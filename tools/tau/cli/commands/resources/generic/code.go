package generic

import (
	"os"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/lib/codefile"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/taubyte/tau/tools/tau/templates"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
)

var codeFlags = []cli.Flag{flags.Template, flags.Language, flags.UseCodeTemplate}

// seedFromTemplate offers a code template and, if one is picked, pre-fills the
// document from the template's own resource yaml. Returns the template url to
// scaffold from, empty when the user declined.
func (l link) seedFromTemplate(ctx *cli.Context, doc tcc.Doc) (string, error) {
	if !ctx.IsSet(flags.Template.Name) && !prompts.GetUseACodeTemplate(ctx) {
		return "", nil
	}

	language, err := prompts.GetOrSelectLanguage(ctx)
	if err != nil {
		return "", err
	}
	templateMap, err := templates.Get().CodeTemplates(l.group.Dir, language)
	if err != nil {
		return "", err
	}
	url, err := prompts.SelectATemplate(ctx, templateMap)
	if err != nil {
		return "", err
	}

	raw, err := os.ReadFile(url + "/config.yaml")
	if err != nil {
		return "", err
	}
	seed := map[string]any{}
	if err := yaml.Unmarshal(raw, &seed); err != nil {
		return "", err
	}
	for k, v := range seed {
		if k == "id" || k == "name" {
			continue
		}
		doc[k] = v
	}
	return url, nil
}

// scaffold writes the resource's code directory from a template.
func (l link) scaffold(name, templateURL string) error {
	if templateURL == "" {
		return nil
	}
	p, err := codefile.Path(name, l.group.Dir)
	if err != nil {
		return err
	}
	return p.Write(templateURL, name)
}

// removeCode drops the resource's code directory.
func (l link) removeCode(name string) error {
	p, err := codefile.Path(name, l.group.Dir)
	if err != nil {
		return err
	}
	return os.RemoveAll(p.String())
}
