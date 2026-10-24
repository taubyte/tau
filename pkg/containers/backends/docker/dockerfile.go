package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/frontend/dockerfile/command"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/taubyte/tau/pkg/containers/reserve"
)

// restrictContext is what a restricted build sends the daemon: the build
// context spooled under dir, its Dockerfile checked, and the Dockerfile's base
// images pinned to the ones whose build triggers were checked.
//
// A RESTRICTED BUILD'S EGRESS IS ITS NETWORK, and the daemon fetches some things
// itself, in its own network namespace, which the firewall does not and cannot
// cover. With the classic builder, which a restricted build is pinned to, that is
// the source of an ADD that is a URL once its variables are expanded; and an ADD
// runs wherever an ONBUILD trigger runs, which is at the FROM of a later stage
// built from the stage that declared it, and at the FROM of an image that carries
// one. So such an ADD is refused in the Dockerfile, in its ONBUILD triggers, and in
// the triggers of every base image. A base image is pulled from a registry the
// same way the image of any container is, by the daemon's registry
// configuration; that is not the build reaching the network, and is not checked.
//
// THE BASE IMAGE IS CHECKED BY ID AND BUILT BY ID: a tag can be pulled or retagged
// between the check and the build, and the daemon would build FROM an image whose
// triggers nobody read. The Dockerfile is appended to the context with each FROM
// naming the image that was checked, on the lines the original FROM took, at the
// name the daemon opens it at. A build that copies its Dockerfile into the image
// copies that one.
func (b *DockerBackend) restrictContext(ctx context.Context, buildContext io.Reader, name, dir string, floor reserve.Floor) (*buildContext, error) {
	spooled, err := spoolContext(buildContext, dir, floor)
	if err != nil {
		return nil, err
	}
	if err := b.pinDockerfile(ctx, spooled, name); err != nil {
		spooled.remove()
		return nil, err
	}
	return spooled, nil
}

func (b *DockerBackend) pinDockerfile(ctx context.Context, spooled *buildContext, name string) error {
	header, dockerfile, at, err := spooled.dockerfile(name)
	if err != nil || header == nil {
		return err
	}
	pinned, err := b.pin(ctx, dockerfile)
	if err != nil {
		return err
	}
	return spooled.put(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     at,
		Mode:     header.Mode,
		Uid:      header.Uid,
		Gid:      header.Gid,
		Uname:    header.Uname,
		Gname:    header.Gname,
		ModTime:  header.ModTime,
		Size:     int64(len(pinned)),
	}, bytes.NewReader(pinned), nil)
}

// pin answers the Dockerfile with each FROM naming what was checked.
//
// Every FROM is written out again, an external image as its id, a stage and
// scratch as their names, with nothing left for the daemon to expand: what the
// daemon resolves is then what was resolved here, whatever its parser version
// makes of a variable.
func (b *DockerBackend) pin(ctx context.Context, dockerfile []byte) ([]byte, error) {
	parsed, err := checkDockerfile(dockerfile)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(dockerfile), "\n")
	lex := shell.NewLex(parsed.EscapeToken)
	stages := map[string]bool{}
	var (
		meta     []string
		seenFrom bool
	)
	for _, n := range parsed.AST.Children {
		switch strings.ToLower(n.Value) {
		case command.Arg:
			if !seenFrom {
				if meta, err = metaArgs(lex, n, meta); err != nil {
					return nil, err
				}
			}
		case command.From:
			seenFrom = true
			from, stage, err := b.from(ctx, lex, n, meta, stages)
			if err != nil {
				return nil, err
			}
			lines[n.StartLine-1] = from
			for line := n.StartLine; line < n.EndLine; line++ {
				lines[line] = ""
			}
			if stage != "" {
				stages[stage] = true
			}
		}
	}

	pinned := []byte(strings.Join(lines, "\n"))
	reparsed, err := checkDockerfile(pinned)
	if err != nil {
		return nil, err
	}
	if len(reparsed.AST.Children) != len(parsed.AST.Children) {
		return nil, errors.New("restricted egress: the Dockerfile with its base images pinned reads as other instructions")
	}
	return pinned, nil
}

// metaArgs adds an ARG before the first FROM to what a FROM is expanded with:
// its default, expanded with the ones before it. A build here passes no build
// arguments, so the default is the value.
func metaArgs(lex *shell.Lex, n *parser.Node, meta []string) ([]string, error) {
	for word := n.Next; word != nil; word = word.Next {
		key, value, ok := strings.Cut(word.Value, "=")
		if !ok {
			continue
		}
		value, err := lex.ProcessWord(value, meta)
		if err != nil {
			return nil, fmt.Errorf("restricted egress: expanding `%s`: %w", n.Original, err)
		}
		meta = append(meta, key+"="+value)
	}
	return meta, nil
}

// stageName is what the daemon takes as the name of a build stage.
var stageName = regexp.MustCompile(`^[a-z][a-z0-9-_.]*$`)

// from resolves one FROM the way the classic builder does: an earlier stage of
// that name, scratch, or an image, used where it is and pulled where it is not.
// It answers the FROM to build instead and the name of the stage it begins.
func (b *DockerBackend) from(ctx context.Context, lex *shell.Lex, n *parser.Node, meta []string, stages map[string]bool) (string, string, error) {
	var words []string
	for word := n.Next; word != nil; word = word.Next {
		words = append(words, word.Value)
	}
	var stage string
	switch {
	case len(words) == 1:
	case len(words) == 3 && strings.EqualFold(words[1], "AS"):
		stage = strings.ToLower(words[2])
		if !stageName.MatchString(stage) || stages[stage] {
			return "", "", fmt.Errorf("restricted egress: `%s` names its stage in a way the daemon refuses", n.Original)
		}
	default:
		return "", "", fmt.Errorf("restricted egress: `%s` is not `FROM image [AS name]`", n.Original)
	}

	base, err := lex.ProcessWord(words[0], meta)
	if err != nil || base == "" {
		return "", "", fmt.Errorf("restricted egress: `%s` names no base image (%v)", n.Original, err)
	}

	var (
		platform *ocispec.Platform
		flags    string
	)
	if len(n.Flags) > 1 {
		return "", "", fmt.Errorf("restricted egress: `%s` takes one flag, --platform", n.Original)
	}
	for _, flag := range n.Flags {
		value, ok := strings.CutPrefix(flag, "--platform=")
		if !ok {
			return "", "", fmt.Errorf("restricted egress: `%s` takes one flag, --platform", n.Original)
		}
		value, err := lex.ProcessWord(value, meta)
		if err != nil {
			return "", "", fmt.Errorf("restricted egress: expanding `%s`: %w", n.Original, err)
		}
		parsed, err := platforms.Parse(value)
		if err != nil {
			return "", "", fmt.Errorf("restricted egress: `%s`: %w", n.Original, err)
		}
		platform = &parsed
		flags = " --platform=" + platforms.Format(parsed)
	}

	switch {
	case stages[strings.ToLower(base)]:
		base = strings.ToLower(base)
	case base == "scratch":
	default:
		if base, err = b.checkedBase(ctx, base, platform); err != nil {
			return "", "", err
		}
	}

	from := "FROM" + flags + " " + base
	if stage != "" {
		from += " AS " + stage
	}
	return from, stage, nil
}

// checkedBase answers the id of the image a stage builds FROM, once the build
// triggers it carries are checked.
func (b *DockerBackend) checkedBase(ctx context.Context, name string, platform *ocispec.Platform) (string, error) {
	var options []client.ImageInspectOption
	var pull []ocispec.Platform
	if platform != nil {
		options = append(options, client.ImageInspectWithPlatform(platform))
		pull = append(pull, *platform)
	}
	image, err := b.client.ImageInspect(ctx, name, options...)
	if err != nil {
		if err := b.pull(ctx, name, pull); err != nil {
			return "", fmt.Errorf("restricted egress: base image: %w", err)
		}
		if image, err = b.client.ImageInspect(ctx, name, options...); err != nil {
			return "", fmt.Errorf("restricted egress: base image %s: %w", name, err)
		}
	}
	if image.ID == "" {
		return "", fmt.Errorf("restricted egress: base image %s has no id", name)
	}
	if image.Config != nil {
		for _, trigger := range image.Config.OnBuild {
			if err := checkTrigger(trigger); err != nil {
				return "", fmt.Errorf("base image %s: %w", name, err)
			}
		}
	}
	return image.ID, nil
}

// checkTrigger checks a base image's build trigger as the daemon runs it: parsed
// on its own as a Dockerfile of one instruction.
func checkTrigger(trigger string) error {
	if strings.Contains(trigger, "\n") {
		return fmt.Errorf("restricted egress cannot check the build trigger %q: it spans lines, which no ONBUILD it passes records", trigger)
	}
	_, err := checkDockerfile([]byte(trigger))
	return err
}

// checkDockerfile parses a Dockerfile as the daemon does and refuses one that
// can make the daemon fetch outside the build's network, or that the daemon's
// parser could read as other instructions than this one does.
//
// THE PARSER IS buildkit's, the one the daemon embeds, at an older version than
// any current daemon's. Where its versions differ in which lines make one
// instruction, the Dockerfile is refused rather than read one way:
//
//   - the escape character is set by a parser directive, and which comment lines
//     are directives has loosened release by release, so a directive-like
//     comment this parser does not apply is refused (see directives);
//   - which lines a heredoc takes has widened release by release, so an
//     instruction holding "<<" is refused, since a heredoc cannot start without it;
//   - an instruction this parser does not know is refused rather than ignored.
//
// Within an instruction, versions also differ in how flags and JSON are split
// into words; an ADD is held to its whole text as well as to its words (see
// fetches), so no reading of them finds a URL that this did not.
func checkDockerfile(dockerfile []byte) (*parser.Result, error) {
	if err := directives(dockerfile); err != nil {
		return nil, err
	}
	parsed, err := parser.Parse(bytes.NewReader(dockerfile))
	if err != nil {
		return nil, fmt.Errorf("restricted egress: parsing the Dockerfile: %w", err)
	}
	for _, n := range parsed.AST.Children {
		if err := checkInstruction(n); err != nil {
			return nil, err
		}
	}
	return parsed, nil
}

func checkInstruction(n *parser.Node) error {
	keyword := strings.ToLower(n.Value)
	if _, known := command.Commands[keyword]; !known {
		return fmt.Errorf("restricted egress cannot check `%s`: the check does not know the instruction", n.Original)
	}
	switch {
	case strings.Contains(n.Original, "<<"):
		return fmt.Errorf("restricted egress cannot check `%s`: versions of the daemon's parser disagree on the lines a heredoc takes", n.Original)
	case keyword == command.Add && fetches(n):
		return fmt.Errorf("restricted egress cannot cover `%s`: the daemon fetches an ADD source that is a URL, or a variable that holds one, outside the build's network; use COPY for files in the context", n.Original)
	case keyword == command.Onbuild && n.Next != nil && len(n.Next.Children) == 1:
		if err := checkInstruction(n.Next.Children[0]); err != nil {
			return fmt.Errorf("ONBUILD runs it in the build of any stage FROM this one: %w", err)
		}
	}
	return nil
}

// unquoted drops what the daemon's word processing can remove from a word:
// quotes, and either escape character.
var unquoted = strings.NewReplacer(`"`, "", `'`, "", `\`, "", "`", "")

// fetches reports whether an ADD can name a URL. The daemon fetches a source
// that starts with http:// or https:// once its quotes, escapes and variables
// are processed, so a "$" anywhere, or a "://" left once the quotes and escapes
// are dropped, is taken for one. That is asked of the instruction as written, so
// no version's reading of its words or flags can find a URL that was missed, and
// of each word, which is where JSON's escapes are decoded.
func fetches(n *parser.Node) bool {
	texts := append([]string{n.Original}, n.Flags...)
	for word := n.Next; word != nil; word = word.Next {
		texts = append(texts, word.Value)
	}
	for _, text := range texts {
		if strings.Contains(text, "$") || strings.Contains(unquoted.Replace(text), "://") {
			return true
		}
	}
	return false
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// directives refuses a Dockerfile with a comment line that reads as a parser
// directive to some version of the daemon's parser and was not applied by this
// one. Directives are read from the first lines until one is not a directive,
// and each release reads more lines as one: an unknown key ends the directives
// for this parser and not for one that knows the key, and a later release takes
// any whitespace after the "#". So the escape character the daemon reads could
// be another than this parser read, and with it where every instruction ends.
func directives(dockerfile []byte) error {
	var applied parser.DirectiveParser
	for i, line := range bytes.Split(dockerfile, []byte("\n")) {
		if i == 0 {
			line = bytes.TrimPrefix(line, utf8BOM)
		}
		line = bytes.TrimLeftFunc(bytes.TrimRight(line, "\r\n"), unicode.IsSpace)
		directive, err := applied.ParseLine(line)
		if err != nil {
			return fmt.Errorf("restricted egress: parsing the Dockerfile: %w", err)
		}
		if directive != nil {
			continue
		}
		if directiveLike(line) {
			return fmt.Errorf("restricted egress cannot check a Dockerfile starting %q: versions of the daemon's parser disagree on whether it is a parser directive", line)
		}
		return nil
	}
	return nil
}

// directiveLike reports whether a line is "#", spaces, a key, spaces and "=",
// which is looser than any version's directive.
func directiveLike(line []byte) bool {
	rest, ok := bytes.CutPrefix(line, []byte("#"))
	if !ok {
		return false
	}
	rest = bytes.TrimLeftFunc(rest, unicode.IsSpace)
	key := bytes.IndexFunc(rest, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if key <= 0 {
		return false
	}
	return bytes.HasPrefix(bytes.TrimLeftFunc(rest[key:], unicode.IsSpace), []byte("="))
}
