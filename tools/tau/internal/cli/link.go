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
// EVERY CREDENTIAL IS MADE HERE, on the developer's machine, and installed with
// the developer's own token. The cloud holds no forge token, so it cannot
// install a webhook or a deploy key itself; what it holds is the private half
// of each, in the secrets service, under pkg/linkcreds' names:
//
//  1. read the project's id from project.yaml — the cloud mints none
//  2. ask each repository's forge for its canonical spelling, which is what a
//     forge's deliveries will name it as
//  3. claim the id for the configuration repository and attach the code
//     repositories (POST /projects/<id>); an id somebody else claimed is
//     refused by name, and nothing below runs
//  4. generate one webhook secret for the project and one ed25519 deploy key
//     per repository, and store their private halves on the cloud
//  5. install, on each repository, a push webhook pointing at
//     <cloud>/hook/<id> and the deploy key, read-only
//
// --manual stops after 4 and prints what step 5 would have installed, for a
// forge this CLI cannot reach or a token that may not manage hooks.

func linkCommand(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "claim this project's id on the cloud and install its webhook and deploy keys",
		Long: "The id is project.yaml's own. The token must write to every repository named; it is also\n" +
			"what installs the webhook and the read-only deploy keys on the forge (--manual prints them\n" +
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

			check := func(s string) error { _, err := repoKey(s); return err }
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
				spelled, err := repoKey(r)
				if err != nil {
					return err
				}
				k, err := repokey.Parse("https://" + spelled)
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

			sessions := make([]*forge.Session, len(keys))
			if !manual {
				for i, k := range keys {
					s, err := forges.For(k, forge.Token(cl.Token()))
					if err != nil {
						return fmt.Errorf("%s: %w (or run with --manual)", k, err)
					}
					if err := s.Require(forge.CapPushHooks, forge.CapDeployKeys); err != nil {
						return fmt.Errorf("%s: %w (or run with --manual)", k, err)
					}
					// THE FORGE'S SPELLING, because a delivery names the
					// repository the way the forge does and the cloud
					// compares it exactly.
					repo, err := s.Repository(cmd.Context(), k)
					if err != nil {
						return fmt.Errorf("reading %s on its forge: %w", k, err)
					}
					// ASKED BEFORE ANYTHING IS CLAIMED: installing a webhook
					// and a deploy key takes admin on most forges, and a link
					// that claimed the id and then could not install would
					// leave a project nobody can push to.
					access, err := s.Access(cmd.Context(), k)
					if err != nil {
						return fmt.Errorf("reading your access to %s: %w", k, err)
					}
					if !access.CanAdmin() {
						return fmt.Errorf("your token holds %s on %s, and installing a webhook and a deploy key takes admin (or run with --manual)", access, k)
					}
					keys[i], sessions[i] = repo.Key, s
				}
			}

			// THE CREDENTIALS, AND WHAT --manual WOULD PRINT, BEFORE THE CLAIM,
			// so a forge this CLI cannot sign for refuses the link rather than
			// leaving a claimed id nobody can push to.
			creds, err := newCredentials(keys)
			if err != nil {
				return err
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

			if err := creds.store(cmd.Context(), cl, id); err != nil {
				return fmt.Errorf("storing the link's credentials on the cloud: %w", err)
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
			} else {
				for i, k := range keys {
					if err := creds.install(cmd.Context(), sessions[i], creds.keys[i], callback, cl.Base()); err != nil {
						return err
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
	if err := cl.SetSecret(ctx, id, linkcreds.Environment, linkcreds.HookSecret, c.hook.Expose()); err != nil {
		return err
	}
	for _, k := range c.keys {
		if err := cl.SetSecret(ctx, id, linkcreds.Environment, linkcreds.DeployKey(k.repository.String()), []byte(k.private)); err != nil {
			return err
		}
	}
	return nil
}

// install puts a repository's webhook and deploy key on its forge.
func (c *credentials) install(ctx context.Context, s *forge.Session, d deployKey, callback *url.URL, cloud string) error {
	if _, err := s.RegisterPushHook(ctx, d.repository, callback, c.hook); err != nil {
		return fmt.Errorf("installing the webhook on %s: %w", d.repository, err)
	}
	if _, err := s.InstallDeployKey(ctx, d.repository, "tau "+cloud, d.authz); err != nil {
		return fmt.Errorf("installing the deploy key on %s: %w", d.repository, err)
	}
	return nil
}

// manual is what install would have put on each forge. The secret is the WIRE
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
