// Package friendly turns an error that reaches the user into one plain
// sentence and a hint. The original text is kept for --debug / TAU_DEBUG=1.
package friendly

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/go-github/v71/github"
	"github.com/taubyte/tau/pkg/platformerr"
	"github.com/taubyte/tau/tools/tau/i18n/selection"
	"github.com/taubyte/tau/tools/tau/session"
)

// Debug shows the original error under the friendly one. Set by --debug.
var Debug = os.Getenv("TAU_DEBUG") == "1"

// Cloud names the selected cloud for messages; tests replace it.
var Cloud = func() string {
	if c := session.CloudName(); c != "" {
		return c
	}
	return "the selected cloud"
}

var codes = map[platformerr.Code][2]string{
	platformerr.NotFound:          {"Nothing is deployed at that address.", "check the domain and path, then `tau push` and wait for the build"},
	platformerr.NodeBusy:          {"The node is busy.", "try again in a few seconds"},
	platformerr.ModuleUnavailable: {"The code for that address could not be loaded.", "check the last build with `tau query builds`"},
	platformerr.FunctionError:     {"The function failed while running.", "see its logs with `tau query logs`"},
	platformerr.FunctionTimeout:   {"The function took too long.", "raise its timeout with `tau edit function <name> --timeout`, or make it faster"},
	platformerr.OverQuota:         {"The project is over a limit the cloud operator set.", "reduce the resource named in the error, or ask the operator to raise the limit"},
	platformerr.Rejected:          {"The cloud rejected the request.", "check your login with `tau doctor`"},
	platformerr.Internal:          {"The cloud had an internal error.", "try again; if it repeats, check the node logs"},
}

var (
	codeInBody   = regexp.MustCompile(`"(?:error|code)"\s*:\s*"([a-z_]+)"`)
	projectName  = regexp.MustCompile("project `([^`]+)` not found")
	unknownCloud = regexp.MustCompile("unknown cloud `([^`]*)`")
	universe     = regexp.MustCompile("universe `([^`]+)` (?:was )?not found(?:; Dream has: (.*))?")
)

// Explain returns a sentence and a hint for err, or ok=false if it has none.
func Explain(err error) (msg, hint string, ok bool) {
	text := err.Error()

	c := platformerr.CodeOf(err)
	if c == "" {
		if m := codeInBody.FindStringSubmatch(text); m != nil {
			c = platformerr.Code(m[1])
		}
	}
	if e, found := codes[c]; found {
		return e[0], e[1], true
	}

	var rl *github.RateLimitError
	if errors.As(err, &rl) {
		return "GitHub rate limit reached for your token" + resets(rl.Rate.Reset.Time) + ".", "wait, or log in with another token; see `tau doctor`", true
	}
	var abuse *github.AbuseRateLimitError
	if errors.As(err, &abuse) || strings.Contains(strings.ToLower(text), "rate limit") {
		return "GitHub rate limit reached for your token.", "wait a few minutes; see `tau doctor`", true
	}
	var gh *github.ErrorResponse
	if errors.As(err, &gh) && gh.Response != nil && gh.Response.StatusCode == 401 || strings.Contains(text, "401 Bad credentials") {
		return "GitHub did not accept your login token.", "run `tau login --new`, then `tau doctor`", true
	}

	if m := universe.FindStringSubmatch(text); m != nil {
		if m[2] != "" {
			return fmt.Sprintf("Dream has no universe %q.", m[1]), "select one it has (" + m[2] + ") with `tau select cloud --universe <name>`", true
		}
		return fmt.Sprintf("Dream has no universe %q.", m[1]), "start it with `dream new universe " + m[1] + "` or run `tau doctor`", true
	}

	switch {
	case errors.Is(err, selection.ErrorUserNotFound) || strings.Contains(text, "profile does not exist"):
		return "You are not logged in.", "run `tau login`", true
	case strings.Contains(text, "no cloud selected"):
		return "No cloud is selected.", "run `tau select cloud --universe <name>` for Dream, or `--fqdn <cloud>`", true
	case errors.Is(err, selection.ErrorProjectNotFound):
		return "No project is selected.", "run `tau select project`, or cd into a cloned project", true
	case unknownCloud.MatchString(text):
		return fmt.Sprintf("Unknown cloud type %q in your login.", unknownCloud.FindStringSubmatch(text)[1]), "select the cloud again with `tau select cloud`", true
	case strings.Contains(text, "repository not found") || errors.As(err, &gh) && gh.Response != nil && gh.Response.StatusCode == 404:
		return "GitHub repository not found.", "check the owner/name, and that your login can see it (`tau doctor`)", true
	case strings.Contains(text, "127.0.0.1:1421") && containsAny(text, "connection refused", "dial tcp"):
		return "Could not reach Dream: it is not running.", "start it with `dream start`, then run `tau doctor`", true
	}

	if strings.Contains(text, "project not found") || strings.Contains(text, "project") && strings.Contains(text, "not found") {
		if m := projectName.FindStringSubmatch(text); m != nil {
			return fmt.Sprintf("Project %q is not on cloud %s.", m[1], Cloud()), "import it with `tau import project`, or `tau select project`", true
		}
		return "That project is not on the selected cloud.", "import it with `tau import project`, or `tau select project`", true
	}

	var op *net.OpError
	var dns *net.DNSError
	if errors.As(err, &op) || errors.As(err, &dns) || containsAny(text, "connection refused", "no such host", "network is unreachable", "i/o timeout", "dial tcp") {
		return "Could not reach the cloud.", "check your network, and that Dream is running if you use it; see `tau doctor`", true
	}

	return "", "", false
}

// Error is err as the user should see it.
func Error(err error) error {
	if err == nil {
		return nil
	}
	msg, hint, ok := Explain(err)
	if !ok {
		return err
	}
	if Debug {
		return fmt.Errorf("%s\n  hint: %s\n  debug: %w", msg, hint, err)
	}
	return fmt.Errorf("%s\n  hint: %s\n  (rerun with --debug for details)", msg, hint)
}

func resets(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return "; resets at " + t.Local().Format("15:04")
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
