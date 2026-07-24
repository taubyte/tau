package generic

import (
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/urfave/cli/v2"
)

// fill walks the resource's sections in DSL order and asks for every field that
// applies to the document as it stands — show-when conditions and dynamic
// branches are re-evaluated as answers come in, so choosing an http trigger asks
// the http questions and nothing else.
func (l link) fill(ctx *cli.Context, st *tcc.Store, name string, doc tcc.Doc) error {
	targets := l.showWhenTargets()
	for _, section := range l.sectionOrder() {
		// A field whose show-when points at a discriminator must be asked after
		// it. The DSL orders by document layout, which can put the discriminator
		// last (a domain authors certificate/type after certificate/cert), so a
		// field that other fields depend on is asked first within its section.
		for _, targetFirst := range []bool{true, false} {
			for _, f := range l.form.Fields {
				if f.Section != section || targets[key(f.Path)] != targetFirst {
					continue
				}
				if !l.editable(f) || !l.form.Visible(f, doc) {
					continue
				}
				if err := l.ask(ctx, st, name, doc, f); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// showWhenTargets is the set of field paths some other field's or section's
// show-when depends on — the discriminators.
func (l link) showWhenTargets() map[string]bool {
	out := map[string]bool{}
	for _, f := range l.form.Fields {
		if f.ShowWhen != nil {
			out[key(f.ShowWhen.Path)] = true
		}
	}
	for _, s := range l.form.Sections {
		if s.ShowWhen != nil {
			out[key(s.ShowWhen.Path)] = true
		}
	}
	return out
}

func key(path []string) string { return strings.Join(path, "/") }

// sectionOrder is the DSL's section order, with any field whose section the DSL
// never declared appended last so nothing is silently dropped.
func (l link) sectionOrder() []string {
	var order []string
	seen := map[string]bool{}
	for _, s := range l.form.Sections {
		order = append(order, s.ID)
		seen[s.ID] = true
	}
	for _, f := range l.form.Fields {
		if !seen[f.Section] {
			seen[f.Section] = true
			order = append(order, f.Section)
		}
	}
	return order
}

func (l link) ask(ctx *cli.Context, st *tcc.Store, name string, doc tcc.Doc, f tcc.Field) error {
	label := f.Label + ":"
	path := tcc.WritePath(doc, f)

	switch f.Widget {
	case tcc.WidgetBranchSelect:
		prev := tcc.ActiveBranch(doc, f)
		choice, err := prompts.SelectInterfaceField(ctx, f.Alternatives, f.Flag, label, prev)
		if err != nil {
			return err
		}
		tcc.SwitchBranch(doc, f, choice)
		return nil

	case tcc.WidgetSwitch:
		prev, _ := tcc.Get(doc, path).(bool)
		tcc.Set(doc, path, prompts.GetOrAskForBool(ctx, f.Flag, label, prev))
		return nil

	case tcc.WidgetSelect:
		prev, _ := tcc.Get(doc, path).(string)
		v, err := prompts.SelectInterfaceField(ctx, f.Enum, f.Flag, label, prev)
		if err != nil {
			return err
		}
		tcc.Set(doc, path, v)
		return nil

	case tcc.WidgetRefList:
		prev := stringList(tcc.Get(doc, path))
		options := st.Complete(l.group.Dir, name, f.Path)
		if len(options) == 0 {
			printer.Out.WarningPrintfln("no %s defined yet, skipping %s", f.Ref.Group, f.Label)
			return nil
		}
		tcc.Set(doc, path, listOrNil(prompts.MultiSelect(ctx, prompts.MultiSelectConfig{
			Field: f.Flag, Prompt: label, Options: options, Previous: prev,
		})))
		return nil

	case tcc.WidgetRef:
		prev, _ := tcc.Get(doc, path).(string)
		options := st.Complete(l.group.Dir, name, f.Path)
		if len(options) > 0 {
			v, err := prompts.SelectInterfaceField(ctx, options, f.Flag, label, prev)
			if err != nil {
				return err
			}
			tcc.Set(doc, path, v)
			return l.validate(st, name, f, v)
		}

	case tcc.WidgetList:
		prev := stringList(tcc.Get(doc, path))
		tcc.Set(doc, path, listOrNil(askList(ctx, f.Flag, label, prev)))
		return nil
	}

	prev, _ := tcc.Get(doc, path).(string)
	v := prompts.GetOrAskForAStringValue(ctx, f.Flag, label, prev)
	tcc.Set(doc, path, v)
	return l.validate(st, name, f, v)
}

// validate runs the DSL's own compile-free check on the answer, so a bad
// duration or a reference to a resource that doesn't exist is caught here rather
// than at push time.
// A field at a dynamic (map-keyed) location has no plain path, so tcc's
// compile-free validation cannot address it — those are left to the compiler.
func (l link) validate(st *tcc.Store, name string, f tcc.Field, value any) error {
	if value == "" || len(f.Alternatives) > 0 {
		return nil
	}
	if err := st.ValidateField(l.group.Dir, name, f.Path, value); err != nil {
		return fmt.Errorf("%s: %w", f.Label, err)
	}
	return nil
}

// askList reads a repeatable/comma-separated flag, or prompts for a comma
// separated list.
func askList(ctx *cli.Context, flag, label string, prev []string) []string {
	if ctx.IsSet(flag) {
		return splitList(ctx.StringSlice(flag))
	}
	if prompts.UseDefaults {
		return prev
	}
	var val string
	inp := &survey.Input{Message: label}
	if len(prev) > 0 {
		inp.Default = strings.Join(prev, ", ")
	}
	prompts.AskOne(inp, &val)
	return splitList([]string{val})
}

func splitList(in []string) []string {
	var out []string
	for _, entry := range in {
		for _, s := range strings.Split(entry, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func stringList(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, fmt.Sprint(e))
		}
		return out
	}
	return nil
}

// listOrNil keeps an empty list out of the document (tcc.Set deletes on nil).
func listOrNil(v []string) any {
	if len(v) == 0 {
		return nil
	}
	return v
}
