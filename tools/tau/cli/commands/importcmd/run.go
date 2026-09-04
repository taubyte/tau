package importcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"
	"github.com/urfave/cli/v2"

	"github.com/taubyte/tau/pkg/importer"
	"github.com/taubyte/tau/pkg/importer/emit"
)

func run(c *cli.Context) error {
	if c.NArg() != 1 {
		return errors.New("import: name one file to import")
	}
	path := c.Args().First()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("import: reading %s: %w", path, err)
	}

	result, err := importOf(c.String(fromFlag.Name), path, data)
	if err != nil {
		return err
	}

	name := c.String(nameFlag.Name)
	if name == "" {
		name = projectNameFor(path)
	}

	// A DRY RUN WRITES TO MEMORY AND STILL REPORTS EVERYTHING. Reporting
	// without emitting would have to duplicate what emit does to know what is
	// missing, and the two copies would disagree — the whole point of the
	// missing list is that it comes from the same validator an editor uses.
	out := c.String(outFlag.Name)
	dst := afero.Fs(afero.NewOsFs())
	if c.Bool(dryRunFlag.Name) {
		dst = afero.NewMemMapFs()
	}

	report, err := emit.To(dst, out, name, result)
	if err != nil {
		return err
	}

	print(c.App.Writer, path, result.Source, out, report, c.Bool(dryRunFlag.Name))
	return nil
}

// projectNameFor is the directory the source file sits in, as a v2 name.
//
// IT IS THE DIRECTORY AND NOT THE FILENAME. Every compose file is called
// `docker-compose.yml`, so naming the project after it would call every
// imported project the same thing.
func projectNameFor(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// A NAME THAT IS ONLY PUNCTUATION IS NOT A NAME. `/` sanitises to `_`,
	// which is a legal v2 name and a terrible project name, so a directory that
	// carries no letters or digits falls back rather than producing one.
	name, _ := importer.Name(filepath.Base(filepath.Dir(abs)))
	if strings.Trim(name, "_") == "" {
		return "imported"
	}
	return name
}

// print is the report, and the order is deliberate: what cannot be done at all,
// then what has to be decided, then what is subtly different, then what needs
// an account connection.
func print(w interface{ Write([]byte) (int, error) }, path, source, out string, report emit.Report, dry bool) {
	say := func(format string, args ...any) { fmt.Fprintf(w, format+"\n", args...) }

	say("")
	say("read %s as %s", path, source)

	if dry {
		say("dry run: nothing was written")
	} else {
		say("wrote %s into %s", plural(len(report.Written), "file"), out)
	}
	for _, file := range report.Written {
		say("    %s", file)
	}

	if report.Compiles {
		say("")
		say("it compiles.")
	} else {
		say("")
		say("it does not compile yet: %s", firstLine(report.Why))
	}

	section(say, "blocked — the source does something tau cannot express", findingLines(report.Blocked()))
	section(say, "missing — a required field the source does not name", missingLines(report.Missing))
	section(say, "approximated — it carried, and it does not mean the same thing",
		findingLines(atLevel(report.Findings, importer.Approximated)))
	section(say, "elsewhere — real values that are not in this file",
		findingLines(atLevel(report.Findings, importer.Elsewhere)))

	if len(report.Deferred) > 0 {
		section(say, "to be checked against the world", report.Deferred)
	}
}

func section(say func(string, ...any), title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	say("")
	say("%s (%d)", title, len(lines))
	for _, line := range lines {
		say("    %s", line)
	}
}

func findingLines(findings []importer.Finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.String())
	}
	return out
}

func missingLines(missing []emit.Missing) []string {
	out := make([]string, 0, len(missing))
	for _, m := range missing {
		out = append(out, m.String())
	}
	return out
}

func atLevel(findings []importer.Finding, level importer.Level) []importer.Finding {
	var out []importer.Finding
	for _, f := range findings {
		if f.Level == level {
			out = append(out, f)
		}
	}
	return out
}

func plural(n int, thing string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, thing)
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

// firstLine keeps a compile error to one line here; the whole of it is what
// `tau validate config` prints.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
