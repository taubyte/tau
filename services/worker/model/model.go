// Package model backs `taubyte:model/inference`: an adapter between a guest that
// knows a model's name and providers that each want a different envelope, credential
// header and answer shape. Everything about where inference happens is on the model
// resource, carried in the grant; a workload passes only a name.
//
// Formats: `openai` and `ollama` share a shape; `anthropic` puts the system turn at
// the top level, requires `max_tokens` and answers in content blocks. A provider that
// answered no is refused (retrying fails the same way); a failed dial, timeout or 5xx
// is unavailable. The credential is read from the vault at the call and never logged.
package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/specs"
)

// DefaultTimeout bounds one call to a provider.
//
// A LONG ANSWER IS SLOW BY DESIGN, so this is generous: a model asked for two
// thousand tokens takes tens of seconds and is not stuck. What it stops is a
// provider that accepted a connection and never answers, which would otherwise
// hold a pooled instance for as long as the process lives.
const DefaultTimeout = 5 * time.Minute

var (
	// ErrNoEndpoint is a model resource this node cannot reach: a remote
	// with no endpoint, or a local one nothing here resolves yet.
	ErrNoEndpoint = errors.New("model: that model declares nowhere to send inference")

	// ErrUnknownFormat is a wire format this node does not speak. The
	// vocabulary's set is closed, so this is a resource compiled against a
	// newer one.
	ErrUnknownFormat = errors.New("model: that model speaks a format this node does not")
)

// Workers opens a connection to one of a workload's named ports.
type Workers interface {
	Conn(ctx context.Context, at specs.Address, port string) (net.Conn, error)
}

// Secrets resolves the credential a model resource names. It is the same
// shape worker.Secrets has, declared here because this is the consumer.
type Secrets interface {
	Secret(ctx context.Context, project, environment, name string) (string, error)
}

// Inferences is the adapter, holding a secret resolver and a placement router as
// functions read at the call: the wasm tier is composed before placement. Each may
// answer nil, never a typed nil in an interface.
type Inferences struct {
	secrets func() Secrets
	workers func() Workers
}

var _ wasmtier.Inferences = (*Inferences)(nil)

// New wires the adapter.
//
// BOTH FUNCTIONS ARE REQUIRED and what they ANSWER may be nil. The composition
// always has a question to ask about each — "does this node resolve a secret",
// "does it reach a workload" — and the answer changes while services open, which
// is the whole reason they are functions.
func New(secrets func() Secrets, workers func() Workers) (*Inferences, error) {
	if secrets == nil {
		return nil, errors.New("model: no way to ask whether a credential can be read")
	}
	if workers == nil {
		return nil, errors.New("model: no way to ask whether a workload can be reached")
	}
	return &Inferences{secrets: secrets, workers: workers}, nil
}

// Infer calls the provider the grant names.
func (i *Inferences) Infer(ctx context.Context, grant invoke.Grant, req *invoke.Inference) (*invoke.Answer, error) {
	endpoint, client, err := i.reach(grant)
	if err != nil {
		return nil, err
	}

	format := grant.Settings[specs.ModelKeyFormat]
	body, err := providerRequest(format, grant, req)
	if err != nil {
		return nil, err
	}

	call, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	post, err := http.NewRequestWithContext(call, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("model: %w", err)
	}
	post.Header.Set("content-type", "application/json")

	if err := i.authorize(call, post, format, grant); err != nil {
		return nil, err
	}

	res, err := client.Do(post)
	if err != nil {
		return nil, fmt.Errorf("model: reaching %s: %w", endpoint, err)
	}
	defer res.Body.Close()

	// BOUNDED, because what is on the other end is somebody else's server and a
	// body with no end would be this node's memory.
	answer, err := io.ReadAll(io.LimitReader(res.Body, maxProviderBody))
	if err != nil {
		return nil, fmt.Errorf("model: reading from %s: %w", endpoint, err)
	}

	if res.StatusCode >= 400 {
		return nil, providerError(res.StatusCode, answer)
	}
	return providerAnswer(format, answer)
}

// maxProviderBody is how much of an answer this node will hold.
const maxProviderBody = 8 << 20

// authorize puts the credential where the format expects it.
//
// THE THREE DISAGREE about the header, and getting it wrong is a 401 that reads
// like a bad key: OpenAI takes a bearer token, Anthropic its own header plus a
// version, and ollama takes nothing at all.
func (i *Inferences) authorize(ctx context.Context, post *http.Request, format string, grant invoke.Grant) error {
	name := grant.Settings[specs.ModelInferenceKeyRemoteCredentialsName]
	if name == "" {
		// A local model, or a provider that needs none. Both are legitimate:
		// ollama on the same machine wants no credential.
		return nil
	}

	project, environment, ok := deploymentOf(grant.Handle)
	if !ok {
		return fmt.Errorf("model: %q does not name a deployment", grant.Handle)
	}
	vault := i.secrets()
	if vault == nil {
		return errors.New("model: that model names a credential and this node reads no secret")
	}

	value, err := vault.Secret(ctx, project, environment, name)
	if err != nil {
		return fmt.Errorf("model: reading that model's credential: %w", err)
	}

	switch format {
	case specs.ModelFormatAnthropic:
		post.Header.Set("x-api-key", value)
		post.Header.Set("anthropic-version", anthropicVersion)
	default:
		post.Header.Set("authorization", "Bearer "+value)
	}
	return nil
}

// anthropicVersion is the API version this node's request shape is written
// against. It is pinned rather than omitted: the header is required, and a
// provider that defaulted it would be free to change what this node sends.
const anthropicVersion = "2023-06-01"

// reach is where this model's inference goes, and what to send it with.
//
// # A remote model is a URL and a local one is a connection
//
// The difference is only in the dial. A workload serving inference speaks the
// same HTTP as a provider does — it is running ollama or vllm — so what changes
// is how the socket is opened, not what is written on it. So a local model gets
// a client whose dialler goes through placement and a URL whose host is a
// placeholder nothing resolves.
//
// THE PLACEHOLDER IS NOT A HOSTNAME ANYWHERE. The dialler ignores it entirely,
// and it is `.invalid` — the TLD reserved for exactly this — so that a bug that
// let it reach a real resolver asks for a name that cannot exist rather than
// one somebody owns.
func (i *Inferences) reach(grant invoke.Grant) (string, *http.Client, error) {
	path := pathFor(grant.Settings[specs.ModelKeyFormat])

	switch grant.Settings[specs.ModelInferenceKeyKind] {
	case specs.ModelInferenceKindRemote:
		base := strings.TrimSuffix(grant.Settings[specs.ModelInferenceKeyRemoteEndpoint], "/")
		if base == "" {
			return "", nil, ErrNoEndpoint
		}
		return base + path, http.DefaultClient, nil

	case specs.ModelInferenceKindLocal:
		at, port, err := localWorkload(grant)
		if err != nil {
			return "", nil, err
		}
		workers := i.workers()
		if workers == nil {
			return "", nil, fmt.Errorf("%w: it is served by a workload and this node reaches none", ErrNoEndpoint)
		}

		client := &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return workers.Conn(ctx, at, port)
			},
			// ONE CONNECTION PER CALL. Placement decides which node answers,
			// and a pooled connection would pin every later call of this
			// instance to whichever node took the first.
			DisableKeepAlives: true,
		}}
		return "http://model.invalid" + path, client, nil
	}
	return "", nil, ErrNoEndpoint
}

// localWorkload is which workload serves this model, and on which of its ports.
func localWorkload(grant invoke.Grant) (specs.Address, string, error) {
	name := grant.Settings[specs.ModelInferenceKeyLocalName]
	if name == "" {
		return specs.Address{}, "", fmt.Errorf("%w: it is served locally and names no workload", ErrNoEndpoint)
	}

	projectName, environment, ok := deploymentOf(grant.Handle)
	if !ok {
		return specs.Address{}, "", fmt.Errorf("model: %q does not name a deployment", grant.Handle)
	}

	var scope []string
	if joined := grant.Settings[settingLocalScope]; joined != "" {
		scope = strings.Split(joined, "/")
	}

	return specs.Address{
		Project:     projectName,
		Environment: environment,
		Scope:       scope,
		Kind:        specs.KindService,
		Name:        name,
	}, grant.Settings[specs.ModelInferenceKeyLocalPort], nil
}

// settingLocalScope is where the resolved namespace chain travels. The composer
// writes it — see services/worker's localModelScope — because only the compiled
// project can answer where a workload lives.
const settingLocalScope = "inference/local/scope"

// pathFor is where each format's completions live under the base URL.
func pathFor(format string) string {
	switch format {
	case specs.ModelFormatAnthropic:
		return "/v1/messages"
	case specs.ModelFormatOllama:
		return "/v1/chat/completions"
	default:
		return "/v1/chat/completions"
	}
}

// deploymentOf splits a handle back into the deployment it belongs to.
//
// specs.Handle joins a project, an environment and an id with "/", and none
// of the three may contain one — which is what makes this exact rather than a
// guess.
func deploymentOf(handle string) (project, environment string, ok bool) {
	parts := strings.Split(handle, "/")
	if len(parts) < 3 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// providerError turns a refusal into the arm the guest sees.
//
// 4xx IS THE PROVIDER SAYING NO and 5xx is it being unable to answer, which is
// the same split the world's `refused` and `unavailable` arms draw. 429 is the
// one that sits across it and is treated as unavailable: a rate limit is a
// request that would succeed later, which is what a retry is for.
func providerError(status int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	if len(detail) > 512 {
		detail = detail[:512]
	}

	if status >= 500 || status == http.StatusTooManyRequests {
		return fmt.Errorf("model: the provider answered %d: %s", status, detail)
	}
	return fmt.Errorf("%w: %d: %s", wasmtier.ErrRefusedByProvider, status, detail)
}
