// Package doctor checks what tau needs to work and says how to fix each part.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	dreamClient "github.com/taubyte/tau/tools/tau/clients/dream"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/urfave/cli/v2"
)

var Command = &cli.Command{
	Name:  "doctor",
	Usage: "Check login, cloud, Docker and Dream, with a fix for each problem",
	Action: func(ctx *cli.Context) error {
		profile, _ := loginLib.GetSelectedProfile()
		return report(ctx.App.Writer, run(profile, live(ctx.Context)))
	},
}

type result struct{ status, name, msg, fix string }

// deps are the outside calls doctor makes; tests replace them.
type deps struct {
	github       func(token string) (http.Header, int, error) // one GET /user
	universes    func() ([]string, error)
	docker       func() error
	lookup       func(host string) error
	dreamVersion func() (string, error)
	dreamAPI     func() (string, error) // the running Dream's GET /version
	now          func() time.Time
	project      func() []string // the selected project's files, "/"-separated; nil: none
}

func live(ctx context.Context) deps {
	return deps{
		github: func(token string) (http.Header, int, error) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
			req.Header.Set("Authorization", "token "+token)
			resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
			if err != nil {
				return nil, 0, err
			}
			resp.Body.Close()
			return resp.Header, resp.StatusCode, nil
		},
		universes: func() ([]string, error) {
			c, err := dreamClient.Client(ctx)
			if err != nil {
				return nil, err
			}
			m, err := c.Universes()
			var names []string
			for n := range m {
				names = append(names, n)
			}
			slices.Sort(names)
			return names, err
		},
		docker: func() error {
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			return exec.CommandContext(ctx, "docker", "info").Run()
		},
		lookup: func(host string) error { _, err := net.LookupHost(host); return err },
		dreamVersion: func() (string, error) {
			out, err := exec.Command("dream", "--version").Output()
			return strings.TrimSpace(string(out)), err
		},
		dreamAPI: func() (string, error) {
			c, err := dreamClient.Client(ctx)
			if err != nil {
				return "", err
			}
			v, err := c.Version()
			return v.String(), err
		},
		now:     time.Now,
		project: projectFiles,
	}
}

// projectFiles lists every file of the selected project's repos (without .git).
func projectFiles() (files []string) {
	name, err := config.GetSelectedProject()
	if err != nil {
		return nil
	}
	p, err := config.Projects().Get(name)
	if err != nil || p.Location == "" {
		return nil
	}
	filepath.WalkDir(p.Location, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if err == nil && !d.IsDir() {
			files = append(files, filepath.ToSlash(path))
		}
		return nil
	})
	return
}

var localtauName = regexp.MustCompile(`[A-Za-z0-9.-]+\.localtau`)

// checkProject warns about what breaks a project on Windows: Dream names the
// hosts file does not map, paths near MAX_PATH, file names that differ by case.
func checkProject(files []string, lookup func(string) error, add func(status, name, msg, fix string)) {
	names, long, seen := map[string]bool{}, []string{}, map[string]string{}
	var clashes []string
	for _, f := range files {
		if len(f) >= 240 { // Windows MAX_PATH is 260 and builds add to it
			long = append(long, f)
		}
		if other, ok := seen[strings.ToLower(f)]; ok && other != f {
			clashes = append(clashes, other+" / "+f)
		}
		seen[strings.ToLower(f)] = f
		if strings.HasSuffix(f, ".yaml") && strings.Contains(f, "/config/") {
			if data, err := os.ReadFile(f); err == nil {
				for _, n := range localtauName.FindAllString(string(data), -1) {
					names[n] = true
				}
			}
		}
	}
	var unresolved []string
	for _, n := range slices.Sorted(maps.Keys(names)) {
		if lookup(n) != nil {
			unresolved = append(unresolved, n)
		}
	}
	if len(unresolved) > 0 {
		add("WARN", "localtau", "does not resolve: "+strings.Join(unresolved, ", "),
			"add them to the hosts file (Windows and WSL have one each), or call the gateway with a Host header: curl -H 'Host: "+unresolved[0]+"' http://127.0.0.1:<gateway port>/")
	}
	if len(long) > 0 {
		add("WARN", "long paths", fmt.Sprintf("%d path(s) near the 260-char Windows limit, e.g. %s", len(long), long[0]),
			"move the project to a shorter folder, or enable Windows long paths and `git config --global core.longpaths true`")
	}
	if len(clashes) > 0 {
		add("WARN", "file names", "names differing only by case: "+strings.Join(clashes, ", "),
			"rename one of each pair: Windows and macOS see them as the same file")
	}
}

func run(p config.Profile, d deps) (out []result) {
	add := func(status, name, msg, fix string) { out = append(out, result{status, name, msg, fix}) }

	if p.Token == "" {
		add("FAIL", "login", "no login token", "run `tau login`")
	} else if h, code, err := d.github(p.Token); err != nil {
		add("FAIL", "login", "GitHub unreachable: "+err.Error(), "check your network")
	} else {
		switch code {
		case http.StatusOK:
			add("PASS", "login", "token valid for "+p.GitUsername, "")
		case http.StatusUnauthorized:
			add("FAIL", "login", "GitHub rejects the token", "run `tau login --new`")
		default:
			add("WARN", "login", fmt.Sprintf("GitHub answered %d", code), "retry later")
		}

		left, _ := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
		reset := ""
		if s, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			reset = "; resets at " + time.Unix(s, 0).Local().Format("15:04")
		}
		switch {
		case h.Get("X-RateLimit-Remaining") == "":
			add("WARN", "rate limit", "GitHub sent no rate limit", "")
		case left == 0:
			add("FAIL", "rate limit", "no GitHub calls left"+reset, "wait, or log in with another token")
		case left < 100:
			add("WARN", "rate limit", fmt.Sprintf("%d GitHub calls left%s", left, reset), "avoid scripts that poll the cloud")
		default:
			add("PASS", "rate limit", fmt.Sprintf("%d GitHub calls left", left), "")
		}

		if date, err := http.ParseTime(h.Get("Date")); err == nil {
			skew := d.now().Sub(date).Round(time.Second)
			if skew < 0 {
				skew = -skew
			}
			if skew > time.Minute {
				add("WARN", "clock", fmt.Sprintf("clock is %s off GitHub's", skew), "sync the system clock")
			} else {
				add("PASS", "clock", "in sync with GitHub", "")
			}
		}
	}

	if p.CloudType == common.DreamCloud {
		names, err := d.universes()
		switch {
		case err != nil:
			add("FAIL", "dream", "Dream API not reachable on 127.0.0.1:1421", "start Dream with `dream start`")
		case slices.Contains(names, p.Cloud):
			add("PASS", "dream", "Dream API reachable", "")
			add("PASS", "cloud", "universe "+p.Cloud+" exists", "")
		default:
			add("PASS", "dream", "Dream API reachable", "")
			fix := "start one with `dream new universe " + p.Cloud + "`"
			if len(names) > 0 {
				fix = "run `tau select cloud --universe " + names[0] + "`"
			}
			add("FAIL", "cloud", fmt.Sprintf("universe %q not found; Dream has: %s", p.Cloud, strings.Join(names, ", ")), fix)
		}
		// older Dreams have no /version
		if v, err := d.dreamAPI(); err == nil {
			add("PASS", "dream api", v, "")
		} else {
			add("PASS", "dream api", "version unknown", "")
		}
		if err := d.docker(); err != nil {
			add("FAIL", "docker", "Docker not reachable; Dream builds need it", "start Docker (Docker Desktop on Windows/macOS)")
		} else {
			add("PASS", "docker", "Docker running", "")
		}
	} else if p.Cloud == "" {
		add("FAIL", "cloud", "no cloud selected", "run `tau select cloud`")
	} else if seer := "seer.tau." + p.Cloud; d.lookup(seer) != nil { // the apex may have no record; seer is what tau calls
		add("FAIL", "cloud", "cannot resolve "+seer, "check the FQDN with `tau select cloud --fqdn`")
	} else {
		add("PASS", "cloud", "cloud "+p.Cloud+" resolves ("+seer+")", "")
	}

	if d.project != nil {
		checkProject(d.project(), d.lookup, add)
	}

	add("PASS", "tau", common.VersionLine(), "")
	// dream has no --version flag on some releases: its usage text is no version
	switch v, err := d.dreamVersion(); {
	case errors.Is(err, exec.ErrNotFound):
		status := "PASS"
		if p.CloudType == common.DreamCloud {
			status = "WARN"
		}
		add(status, "dream version", "dream CLI not found", "install it with `npm i -g @taubyte/dream`")
	case err != nil || v == "" || strings.Contains(v, "\n") || strings.Contains(strings.ToLower(v), "usage"):
		add("WARN", "dream version", "dream CLI found, version unknown", "")
	default:
		add("PASS", "dream version", v, "")
	}
	return
}

func report(w io.Writer, results []result) error {
	failed := 0
	for _, r := range results {
		line := fmt.Sprintf("%-4s  %-13s %s", r.status, r.name, r.msg)
		if r.fix != "" {
			line += "\n      fix: " + r.fix
		}
		fmt.Fprintln(w, line)
		if r.status == "FAIL" {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	return nil
}
