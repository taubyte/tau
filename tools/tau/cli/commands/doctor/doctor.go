// Package doctor checks what tau needs to work and says how to fix each part.
package doctor

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
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
	now          func() time.Time
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
		now: time.Now,
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
		if err := d.docker(); err != nil {
			add("FAIL", "docker", "Docker not reachable; Dream builds need it", "start Docker (Docker Desktop on Windows/macOS)")
		} else {
			add("PASS", "docker", "Docker running", "")
		}
	} else if p.Cloud == "" {
		add("FAIL", "cloud", "no cloud selected", "run `tau select cloud`")
	} else if err := d.lookup(p.Cloud); err != nil {
		add("FAIL", "cloud", "cannot resolve "+p.Cloud, "check the FQDN with `tau select cloud --fqdn`")
	} else {
		add("PASS", "cloud", "cloud "+p.Cloud+" resolves", "")
	}

	add("PASS", "tau", common.VersionLine(), "")
	if v, err := d.dreamVersion(); err != nil {
		status := "PASS"
		if p.CloudType == common.DreamCloud {
			status = "WARN"
		}
		add(status, "dream version", "dream CLI not found", "install it with `npm i -g @taubyte/dream`")
	} else {
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
