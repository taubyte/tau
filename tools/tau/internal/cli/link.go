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

// link.go is `tau link`: claim project.yaml's id on a cloud for the project's
// repositories, and give the cloud what it needs to build them.
//
// THE CREDENTIALS ARE MADE HERE, on the developer's machine; WHAT IS
// INSTALLED AT THE FORGE IS INSTALLED BY THE CLOUD, under the forge
// connection the developer's tau token carries. The CLI never holds a forge
// token: the cloud opens the connection for the one request that needs it
// (services/build/frontend/linkapi.go). The cloud keeps the private half of
// each credential in the secrets service, under pkg/linkcreds' names:
//
//  1. read the project's id from project.yaml — the cloud mints none
//  2. ask the cloud to read each repository at its forge: the forge's
//     canonical spelling (what a delivery names it as), whether the
//     connection may install on it, and whether this cloud's hook is there
//  3. claim the id for the configuration repository and attach the code
//     repositories (POST /projects/<id>); an id somebody else claimed is
//     refused by name, and nothing below runs
//  4. generate one webhook secret for the project and one ed25519 deploy key
//     per repository, and store their private halves on the cloud
//  5. ask the cloud to install, on each repository, a push webhook to
//     <cloud>/hook/<id> with the stored secret, and the deploy key's public
//     half, read-only
//
// A REPOSITORY THAT ALREADY HAS A HOOK TO <cloud>/hook/<id> was linked before.
// Installing a second would leave the first verified against a secret the
// cloud no longer holds, so link asks (or takes --replace-hook / --keep-hook):
// replacing has the cloud delete that hook and the deploy key titled for it,
// after the new credentials are stored, and install new ones; keeping
// installs and rotates nothing.
//
// --manual stops after 4 and prints what step 5 would have installed, for a
// forge the cloud cannot reach or a connection that may not manage hooks.

func linkCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "claim this project's id on the cloud and install its webhook and deploy keys",
		Long: "The id is project.yaml's own. You must write to every repository named, through the forge\n" +
			"connected to your session for its host (`tau forge connect`); that connection is also what\n" +
			"installs the webhook and the read-only deploy keys on the forge (--manual prints them\n" +
			"instead). The configuration repository defaults to the directory's git origin. What was\n" +
			"linked is written to " + config.LinkFile + ".",
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
			repos := []string{configRepo}
			repos = append(repos, flagStrings(cmd, "code-repo")...)
			keys := make([]repokey.Key, 0, len(repos))
			for _, r := range repos {
				k, err := repoKey(keyer, r)
				if err != nil {
					return err
				}
				keys = append(keys, k)
			}

			cl, err := client(cmd, env)
			if err != nil {
				return err
			}
			callback, err := url.Parse(cl.Base() + "/hook/" + url.PathEscape(id))
			if err != nil {
				return err
			}
			manual := flagBool(cmd, "manual")
			forges, err := forgesFor(env, callback)
			if err != nil {
				return err
			}

			hooked := make([]bool, len(keys))
			if !manual {
				// THE CLOUD READS EACH REPOSITORY under the session's
				// connection for its host: the profile holds only the tau
				// token, and no forge token is ever handed to it.
				for i, k := range keys {
					in, err := cl.InspectRepository(cmd.Context(), id, k.String())
					if err != nil {
						return fmt.Errorf("reading %s on its forge: %w (connect it with `tau forge connect %s`, or run with --manual)", k, err, k.Host)
					}
					// ASKED BEFORE ANYTHING IS CLAIMED: installing a webhook
					// and a deploy key takes admin on most forges, and a link
					// that claimed the id and then could not install would
					// leave a project nobody can push to.
					if !in.Admin {
						return fmt.Errorf("your connection to %s may not administer %s, and installing a webhook and a deploy key takes admin (or run with --manual)", k.Host, k)
					}
					// THE FORGE'S SPELLING, because a delivery names the
					// repository the way the forge does and the cloud
					// compares it exactly.
					if keys[i], err = repokey.Parse("https://" + in.Repository); err != nil {
						return fmt.Errorf("%s answered %q for %s: %w", k.Host, in.Repository, k, err)
					}
					hooked[i] = in.Hooked
				}
			}
			relinking, err := relinkDecision(cmd, env, keys, hooked)
			if err != nil {
				return err
			}

			// THE CREDENTIALS, AND WHAT --manual WOULD PRINT, BEFORE THE CLAIM,
			// so a forge this CLI cannot sign for refuses the link rather than
			// leaving a claimed id nobody can push to.
			var creds *credentials
			if relinking != keepHook {
				if creds, err = newCredentials(keys); err != nil {
					return err
				}
			}
			var paste []manualInstall
			if manual {
				if paste, err = creds.manual(forges, callback); err != nil {
					return err
				}
			}

			codeKeys := make([]string, 0, len(keys)-1)
			for _, k := range keys[1:] {
				codeKeys = append(codeKeys, k.String())
			}
			linked, err := cl.LinkProject(cmd.Context(), id, keys[0].String(), codeKeys)
			var refusal *api.Error
			if errors.As(err, &refusal) && refusal.Status == http.StatusConflict {
				return errors.New(refusal.Message)
			}
			if err != nil {
				return err
			}

			if creds != nil {
				if err := creds.store(cmd.Context(), cl, id); err != nil {
					return fmt.Errorf("storing the link's credentials on the cloud: %w", err)
				}
			}

			t := theme(env)
			var b strings.Builder
			b.WriteString(t.Done(fmt.Sprintf("linked %s as %s", keys[0], id)))
			for _, c := range codeKeys {
				b.WriteString(t.Muted.Render("  code   "+c) + "\n")
			}

			out := linkOutput{Link: config.Link{ProjectID: id, Name: p.Name(), Profile: flagString(cmd, "profile"), Config: linked.Config, Code: linked.Code}}
			if manual {
				out.Install = paste
				b.WriteString("\nInstall these yourself; nothing was sent to the forge.\n")
				for _, m := range paste {
					fmt.Fprintf(&b, "\n%s\n  webhook      %s (push events, application/json)\n  secret       %s\n  deploy key   %s (read-only)\n",
						t.Accent.Render(m.Repository), m.Webhook, m.Secret, m.DeployKey)
				}
			} else if relinking == keepHook {
				b.WriteString(t.Muted.Render("  kept the webhook and deploy keys already installed; the credentials stored on the cloud are unchanged") + "\n")
			} else {
				for i, k := range keys {
					// THE OLD ONES GO ONLY NOW, after the new credentials are
					// stored: from that moment the old hook's secret is gone
					// from the cloud and its deliveries are refused anyway.
					replace := relinking == replaceHook && hooked[i]
					if err := cl.InstallLink(cmd.Context(), id, k.String(), creds.keys[i].authz, replace); err != nil {
						return fmt.Errorf("installing the webhook and deploy key on %s: %w", k, err)
					}
					if replace {
						b.WriteString(t.Muted.Render("  deleted this cloud's previous webhook and deploy key on "+k.String()) + "\n")
					}
					b.WriteString(t.Muted.Render("  installed a webhook and a read-only deploy key on "+k.String()) + "\n")
				}
			}

			if err := config.WriteLink(dir, out.Link); err != nil {
				return err
			}
			if manual {
				return emit(cmd, env, out, b.String())
			}
			return emit(cmd, env, out.Link, b.String())
		},
	}
	addDir(cmd)
	cmd.Flags().String("config-repo", "", "the configuration repository (clone URL or host/owner/name; default: git origin)")
	cmd.Flags().StringArray("code-repo", nil, "a code repository the project builds; repeatable")
	cmd.Flags().Bool("manual", false, "install nothing on the forge: print the webhook and deploy keys to add by hand")
	cmd.Flags().Bool("replace-hook", false, "when a repository already has this cloud's webhook, delete it and this cloud's deploy key and install new ones")
	cmd.Flags().Bool("keep-hook", false, "when a repository already has this cloud's webhook, keep it and the stored credentials and install nothing")
	cmd.MarkFlagsMutuallyExclusive("replace-hook", "keep-hook")
	return cmd
}

// forgesFor is the injected registry, or the public forges. A cloud reached
// over plain http — a development cloud — gets hooks over plain http, which
// the forge package otherwise refuses because a delivery carries a secret.
func forgesFor(env Env, callback *url.URL) (*forge.Registry, error) {
	if env.Forges != nil {
		return env.Forges, nil
	}
	return public.Registry(forge.Policy{AllowInsecureCallback: callback.Scheme == "http"})
}

// linkOutput is what --manual prints under -o json.
type linkOutput struct {
	config.Link
	Install []manualInstall `json:"install,omitempty"`
}

// manualInstall is what one repository needs installed by hand.
type manualInstall struct {
	Repository string `json:"repository"`
	Webhook    string `json:"webhook"`
	Secret     string `json:"secret"`
	DeployKey  string `json:"deploy_key"`
}

// credentials are one project's webhook secret and each repository's deploy
// key.
type credentials struct {
	hook forge.Secret
	keys []deployKey
}

type deployKey struct {
	repository     repokey.Key
	private, authz string
}

func newCredentials(repos []repokey.Key) (*credentials, error) {
	secret, err := forge.NewSecret()
	if err != nil {
		return nil, err
	}
	c := &credentials{hook: secret}
	for _, k := range repos {
		private, authz, err := newDeployKey("tau " + k.String())
		if err != nil {
			return nil, err
		}
		c.keys = append(c.keys, deployKey{repository: k, private: private, authz: authz})
	}
	return c, nil
}

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
func (c *credentials) store(ctx context.Context, cl *api.Client, id string) error {
	for _, k := range c.keys {
		if err := cl.SetSecret(ctx, id, linkcreds.Environment, linkcreds.HookSecret(k.repository.String()), c.hook.Expose()); err != nil {
			return err
		}
		if err := cl.SetSecret(ctx, id, linkcreds.Environment, linkcreds.DeployKey(k.repository.String()), []byte(k.private)); err != nil {
			return err
		}
	}
	return nil
}

// manual is what the cloud would have installed on each forge. The secret is the WIRE
// form the forge's signing scheme derives, which is what a forge is given and
// signs with — never the stored secret itself.
func (c *credentials) manual(forges *forge.Registry, callback *url.URL) ([]manualInstall, error) {
	out := make([]manualInstall, 0, len(c.keys))
	for _, k := range c.keys {
		d, err := forges.Driver(k.repository.Host)
		if err != nil {
			return nil, err
		}
		sig, ok := d.(forge.Signatures)
		if !ok {
			return nil, fmt.Errorf("%s: this forge's deliveries cannot be verified", k.repository.Host)
		}
		wire, err := sig.Scheme().Wire(c.hook)
		if err != nil {
			return nil, err
		}
		out = append(out, manualInstall{Repository: k.repository.String(), Webhook: callback.String(), Secret: wire, DeployKey: k.authz})
	}
	return out, nil
}

// relink is what to do about hooks a previous link installed.
type relink int

const (
	freshLink   relink = iota // no repository has this cloud's hook
	replaceHook               // delete them, rotate everything, install anew
	keepHook                  // leave them and the stored credentials alone
)

// relinkDecision asks, at a terminal, whether to replace this cloud's hooks
// found on the repositories; elsewhere it takes --replace-hook or --keep-hook
// and refuses without one.
func relinkDecision(cmd *cobra.Command, env Env, keys []repokey.Key, hooked []bool) (relink, error) {
	var with, bare []string
	for i, k := range keys {
		if hooked[i] {
			with = append(with, k.String())
		} else {
			bare = append(bare, k.String())
		}
	}
	if len(with) == 0 {
		return freshLink, nil
	}
	decision := freshLink
	switch {
	case flagBool(cmd, "replace-hook"):
		decision = replaceHook
	case flagBool(cmd, "keep-hook"):
		decision = keepHook
	case prompting(cmd, env):
		yes, err := env.Prompter.Confirm(fmt.Sprintf("%s already has this cloud's webhook from an earlier link. Delete it and its deploy key, and install new ones?", strings.Join(with, ", ")))
		if err != nil {
			return freshLink, err
		}
		decision = keepHook
		if yes {
			decision = replaceHook
		}
	default:
		return freshLink, fmt.Errorf("%s already has this cloud's webhook from an earlier link: pass --replace-hook to delete it and install new credentials, or --keep-hook to keep it", strings.Join(with, ", "))
	}
	// KEEPING IS KEEPING THE PROJECT'S SECRET, which only the cloud holds: a
	// repository without the old hook could only be given one by rotating it.
	if decision == keepHook && len(bare) > 0 {
		return freshLink, fmt.Errorf("keeping the existing webhook keeps the project's stored secret, which this machine does not hold, so %s cannot be given a hook; use --replace-hook", strings.Join(bare, ", "))
	}
	return decision, nil
}
