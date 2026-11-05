package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/public"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/linkcreds"
	"github.com/taubyte/tau/tools/tau/internal/api"
	"github.com/taubyte/tau/tools/tau/internal/config"
	"github.com/taubyte/tau/tools/tau/internal/project"
)

// link.go is `tau link`: register project.yaml's id on a cloud for the project's
// repositories and wire each one, doing everything at the forge with this
// profile's own credential (the cloud never acts at a forge). Per repository it
// generates a webhook secret and an ed25519 deploy key, installs the hook (to
// https://build.<fqdn>/hook/<id>) and the read-only key, and hands the
// cloud the secret and private key under pkg/linkcreds' names. --create creates
// missing repositories; --push pushes the project last, so that push is the first
// build. A repository already holding this cloud's hook is replaced or kept (asked,
// or --replace-hook / --keep-hook). --manual installs nothing and prints what to
// install.

func linkCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "register this project on the cloud and install its webhooks and deploy keys",
		Long: "The id is project.yaml's own. The webhooks and read-only deploy keys are installed by this\n" +
			"CLI with your forge credential (`tau forge connect <host>`), which needs admin on each\n" +
			"repository; --manual prints them instead. Repositories may belong to any user, organization,\n" +
			"group or workspace the cloud's tenancy serves. The configuration repository defaults to the\n" +
			"directory's git origin. What was linked is written to " + config.LinkFile + ".",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := flagString(cmd, "dir")
			p, err := project.Open(dir)
			if err != nil {
				return err
			}
			id := p.ID()
			if !p.IsProject() || id == "" {
				return errors.New("no project id here: run `tau init` first, or pass --dir")
			}

			keyer, err := keysFor(env)
			if err != nil {
				return err
			}
			check := func(s string) error { _, err := repoKey(keyer, s); return err }
			configRepo := flagString(cmd, "config-repo")
			if configRepo == "" {
				configRepo = originOf(cmd.Context(), dir)
			}
			if configRepo == "" {
				if configRepo, err = need(cmd, env, "config-repo", "Configuration repository", check); err != nil {
					return err
				}
			}
			repos := append([]string{configRepo}, flagStrings(cmd, "code-repo")...)
			keys := make([]repokey.Key, 0, len(repos))
			for _, r := range repos {
				k, err := repoKey(keyer, r)
				if err != nil {
					return err
				}
				keys = append(keys, k)
			}

			cfg, name, _, cl, err := current(cmd, env)
			if err != nil {
				return err
			}
			callback, err := hookURL(cmd, cl, id)
			if err != nil {
				return err
			}
			forges, err := forgesFor(env, callback)
			if err != nil {
				return err
			}
			manual := flagBool(cmd, "manual")
			if manual && (flagBool(cmd, "create") || flagBool(cmd, "push")) {
				return errors.New("--manual touches no forge, so it cannot --create or --push")
			}

			at := &forgeWork{forges: forges, creds: forgeCreds{env: env, cfg: cfg, name: name, cl: cl, now: time.Now}, sessions: map[string]*forge.Session{}}
			state := make([]repoState, len(keys))
			if !manual {
				for i := range keys {
					if state[i], err = at.inspect(cmd.Context(), keys[i], callback, flagBool(cmd, "create"), !flagBool(cmd, "public")); err != nil {
						return err
					}
					keys[i] = state[i].key
				}
			}
			relinking, err := relinkDecision(cmd, env, keys, state)
			if err != nil {
				return err
			}

			// THE CREDENTIALS, AND WHAT --manual WOULD PRINT, BEFORE THE CLAIM,
			// so a forge this CLI cannot sign for refuses the link rather than
			// leaving a registered id nobody can push to.
			var creds []repoCreds
			for i, k := range keys {
				if state[i].hooked && relinking == keepHook {
					continue
				}
				c, err := newRepoCreds(k, callback)
				if err != nil {
					return err
				}
				c.replace = state[i].hooked
				creds = append(creds, c)
			}
			var paste []manualInstall
			if manual {
				if paste, err = manualFor(forges, creds, callback); err != nil {
					return err
				}
			}

			codeKeys := make([]string, 0, len(keys)-1)
			for _, k := range keys[1:] {
				codeKeys = append(codeKeys, k.String())
			}
			linked, err := cl.LinkProject(cmd.Context(), id, keys[0].String(), codeKeys)
			if refusal := (*api.Error)(nil); errors.As(err, &refusal) && (refusal.Status == http.StatusConflict || refusal.Status == http.StatusForbidden) {
				return errors.New(refusal.Message)
			}
			if err != nil {
				return err
			}
			for _, c := range creds {
				if err := c.store(cmd.Context(), cl, id); err != nil {
					return fmt.Errorf("storing the link's credentials on the cloud: %w", err)
				}
			}

			t := theme(env)
			var b strings.Builder
			b.WriteString(t.Done(fmt.Sprintf("linked %s as %s", keys[0], id)))
			for _, c := range codeKeys {
				b.WriteString(t.Muted.Render("  code   "+c) + "\n")
			}
			for i, k := range keys {
				if state[i].created {
					b.WriteString(t.Muted.Render("  created "+k.String()) + "\n")
				}
			}

			out := linkOutput{Link: config.Link{ProjectID: id, Name: p.Name(), Profile: flagString(cmd, "profile"), Config: linked.Config, Code: linked.Code}}
			if manual {
				out.Install = paste
				b.WriteString("\nInstall these yourself; nothing was sent to the forge.\n")
				for _, m := range paste {
					fmt.Fprintf(&b, "\n%s\n  webhook      %s (push events, application/json)\n  secret       %s\n  deploy key   %s (read-only)\n",
						t.Accent.Render(m.Repository), m.Webhook, m.Secret, m.DeployKey)
				}
			} else {
				// THE OLD ONES GO ONLY NOW, after the new credentials are
				// stored: from that moment the old hook's secret is gone from
				// the cloud and its deliveries are refused anyway.
				for _, c := range creds {
					if err := at.install(cmd.Context(), c, callback); err != nil {
						return fmt.Errorf("installing the webhook and deploy key on %s: %w", c.repository, err)
					}
					if c.replace {
						b.WriteString(t.Muted.Render("  deleted this cloud's previous webhook and deploy key on "+c.repository.String()) + "\n")
					}
					b.WriteString(t.Muted.Render("  installed a webhook and a read-only deploy key on "+c.repository.String()) + "\n")
				}
				if relinking == keepHook {
					b.WriteString(t.Muted.Render("  kept the webhooks and deploy keys already installed") + "\n")
				}
			}

			if err := config.WriteLink(dir, out.Link); err != nil {
				return err
			}
			if flagBool(cmd, "push") {
				commit, err := at.push(cmd.Context(), dir, state[0])
				if err != nil {
					return fmt.Errorf("pushing %s: %w", keys[0], err)
				}
				out.Pushed = commit
				b.WriteString(t.Muted.Render("  pushed "+commit+" to "+keys[0].String()) + "\n")
			}
			return emit(cmd, env, out, b.String())
		},
	}
	addDir(cmd)
	cmd.Flags().String("config-repo", "", "the configuration repository (clone URL or host/owner/name; default: git origin)")
	cmd.Flags().StringArray("code-repo", nil, "a code repository the project builds; repeatable")
	cmd.Flags().Bool("create", false, "create the repositories that do not exist yet, in the namespace each one names")
	cmd.Flags().Bool("public", false, "with --create, make the new repositories public (default private)")
	cmd.Flags().Bool("push", false, "push this directory to the configuration repository once it is linked")
	cmd.Flags().String("callback", "", "the webhook URL to install (default https://build.<cloud fqdn>/hook/<id>)")
	cmd.Flags().Bool("manual", false, "install nothing on the forge: print the webhooks and deploy keys to add by hand")
	cmd.Flags().Bool("replace-hook", false, "when a repository already has this cloud's webhook, delete it and this cloud's deploy key and install new ones")
	cmd.Flags().Bool("keep-hook", false, "when a repository already has this cloud's webhook, keep it and its stored credentials")
	cmd.MarkFlagsMutuallyExclusive("replace-hook", "keep-hook")
	return cmd
}

// hookURL is where a forge delivers id's pushes: --callback, or the cloud's
// plm service under the name the cloud reports.
func hookURL(cmd *cobra.Command, cl *api.Client, id string) (*url.URL, error) {
	base := flagString(cmd, "callback")
	if base == "" {
		fqdn, err := cl.Fqdn(cmd.Context())
		if err != nil {
			return nil, fmt.Errorf("asking the cloud its name: %w (or pass --callback)", err)
		}
		return url.Parse("https://plm." + fqdn + "/hook/" + url.PathEscape(id))
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("--callback %q is not an http(s) URL", base)
	}
	return u, nil
}

// forgesFor is the injected registry, or the public forges. A plain-http
// callback — a development cloud — is allowed only when asked for by name.
func forgesFor(env Env, callback *url.URL) (*forge.Registry, error) {
	if env.Forges != nil {
		return env.Forges, nil
	}
	return public.Registry(forge.Policy{AllowInsecureCallback: callback.Scheme == "http"})
}

// linkOutput is what link prints under -o json.
type linkOutput struct {
	config.Link
	Pushed  string          `json:"pushed,omitempty"`
	Install []manualInstall `json:"install,omitempty"`
}

// manualInstall is what one repository needs installed by hand.
type manualInstall struct {
	Repository string `json:"repository"`
	Webhook    string `json:"webhook"`
	Secret     string `json:"secret"`
	DeployKey  string `json:"deploy_key"`
}

// repoCreds are one repository's webhook secret and deploy key.
type repoCreds struct {
	repository     repokey.Key
	hook           forge.Secret
	title          string
	private, authz string
	// replace is whether this cloud's earlier hook and key are there to go.
	replace bool
}

func newRepoCreds(k repokey.Key, callback *url.URL) (repoCreds, error) {
	secret, err := forge.NewSecret()
	if err != nil {
		return repoCreds{}, err
	}
	title := deployKeyTitle(callback)
	private, authz, err := newDeployKey(title + " " + k.String())
	if err != nil {
		return repoCreds{}, err
	}
	return repoCreds{repository: k, hook: secret, title: title, private: private, authz: authz}, nil
}

// deployKeyTitle names this cloud's deploy keys, so a relink finds its own.
func deployKeyTitle(callback *url.URL) string { return "tau " + callback.Host }

// newDeployKey is an ed25519 key pair: the private half in OpenSSH PEM, the
// public half as an authorized_keys line.
func newDeployKey(comment string) (private, authz string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(block)), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))), nil
}

// store puts the private halves on the cloud, through the same secret route
// every other secret takes.
func (c repoCreds) store(ctx context.Context, cl *api.Client, id string) error {
	if err := cl.SetSecret(ctx, id, linkcreds.Environment, linkcreds.HookSecret(c.repository.String()), c.hook.Expose()); err != nil {
		return err
	}
	return cl.SetSecret(ctx, id, linkcreds.Environment, linkcreds.DeployKey(c.repository.String()), []byte(c.private))
}

// manualFor is what would have been installed on each forge. The secret is the
// WIRE form the forge's signing scheme derives, which is what a forge is
// given and signs with.
func manualFor(forges *forge.Registry, creds []repoCreds, callback *url.URL) ([]manualInstall, error) {
	out := make([]manualInstall, 0, len(creds))
	for _, c := range creds {
		d, err := forges.Driver(c.repository.Host)
		if err != nil {
			return nil, err
		}
		sig, ok := d.(forge.Signatures)
		if !ok {
			return nil, fmt.Errorf("%s: this forge's deliveries cannot be verified", c.repository.Host)
		}
		wire, err := sig.Scheme().Wire(c.hook)
		if err != nil {
			return nil, err
		}
		out = append(out, manualInstall{Repository: c.repository.String(), Webhook: callback.String(), Secret: wire, DeployKey: c.authz})
	}
	return out, nil
}

// relink is what to do about hooks a previous link installed.
type relink int

const (
	freshLink   relink = iota // no repository has this cloud's hook
	replaceHook               // delete them and install anew
	keepHook                  // leave those repositories alone
)

// relinkDecision asks, at a terminal, whether to replace this cloud's hooks
// found on the repositories; elsewhere it takes --replace-hook or --keep-hook
// and refuses without one.
func relinkDecision(cmd *cobra.Command, env Env, keys []repokey.Key, state []repoState) (relink, error) {
	var with []string
	for i, k := range keys {
		if state[i].hooked {
			with = append(with, k.String())
		}
	}
	switch {
	case len(with) == 0:
		return freshLink, nil
	case flagBool(cmd, "replace-hook"):
		return replaceHook, nil
	case flagBool(cmd, "keep-hook"):
		return keepHook, nil
	case prompting(cmd, env):
		yes, err := env.Prompter.Confirm(fmt.Sprintf("%s already has this cloud's webhook from an earlier link. Delete it and its deploy key, and install new ones?", strings.Join(with, ", ")))
		if err != nil {
			return freshLink, err
		}
		if yes {
			return replaceHook, nil
		}
		return keepHook, nil
	}
	return freshLink, fmt.Errorf("%s already has this cloud's webhook from an earlier link: pass --replace-hook to delete it and install new credentials, or --keep-hook to keep it", strings.Join(with, ", "))
}
