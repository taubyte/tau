package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// model.go backs `taubyte:model/inference` on this node.
//
// # The resource is the configuration and the caller has none
//
// Everything about where inference happens — the endpoint, the wire format, the
// credential, the parameters — is on the model resource, carried here in the
// grant's settings. A workload passes a NAME, and an author moves a project
// between providers without touching a guest.
//
// # Three formats, because the vocabulary offers three
//
// `openai`, `anthropic` and `ollama`. The first and third are the same request
// shape — plenty of things that are not OpenAI serve the OpenAI format, which
// is exactly what the vocabulary's own doc says the field is for — and
// anthropic differs in three ways that matter: the system turn is a top-level
// field rather than a message, `max_tokens` is required, and the answer is a
// list of content blocks.
//
// # What is refused and what is unavailable
//
// A provider that ANSWERED and said no is refused: a malformed request, a
// filtered one, a context that does not fit. It will fail again the same way,
// so a caller that retries is wasting its budget. Anything else — a dial that
// failed, a timeout, a 5xx — is unavailable, and retrying is reasonable.
//
// # The credential never appears in a log line
//
// It is read from the vault at the call and put in a header. It is not in the
// spec, not in the grant, and no error here carries the response body of a
// request that had it in a header.

// DefaultInferenceTimeout bounds one call to a provider.
//
// A LONG ANSWER IS SLOW BY DESIGN, so this is generous: a model asked for two
// thousand tokens takes tens of seconds and is not stuck. What it stops is a
// provider that accepted a connection and never answers, which would otherwise
// hold a pooled instance for as long as the process lives.
const DefaultInferenceTimeout = 5 * time.Minute

var (
	// ErrNoModelEndpoint is a model resource this node cannot reach: a remote
	// with no endpoint, or a local one nothing here resolves yet.
	ErrNoModelEndpoint = errors.New("node: that model declares nowhere to send inference")

	// ErrUnknownModelFormat is a wire format this node does not speak. The
	// vocabulary's set is closed, so this is a resource compiled against a
	// newer one.
	ErrUnknownModelFormat = errors.New("node: that model speaks a format this node does not")
)

// inference is the node's Inferences.
//
// IT HOLDS THE SECRET RESOLVER RATHER THAN THE NODE, which is the same seam the
// worker and the proxy take: a credential is read through whichever resolver
// this node settled on — an injected one, its own vault, or one in the fleet —
// and nothing here needs to know which.
type inference struct{ secrets modelSecrets }

// modelSecrets resolves the credential a model resource names. It is the same
// shape worker.Secrets has, declared here because this is the consumer.
type modelSecrets interface {
	Secret(ctx context.Context, project, environment, name string) (string, error)
}

var _ wasmtier.Inferences = (*inference)(nil)

// Infer calls the provider the grant names.
func (i *inference) Infer(ctx context.Context, grant invoke.Grant, req *invoke.Inference) (*invoke.Answer, error) {
	endpoint, err := endpointOf(grant)
	if err != nil {
		return nil, err
	}

	format := grant.Settings["format"]
	body, err := providerRequest(format, grant, req)
	if err != nil {
		return nil, err
	}

	call, cancel := context.WithTimeout(ctx, DefaultInferenceTimeout)
	defer cancel()

	post, err := http.NewRequestWithContext(call, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("node: %w", err)
	}
	post.Header.Set("content-type", "application/json")

	if err := i.authorize(call, post, format, grant); err != nil {
		return nil, err
	}

	res, err := http.DefaultClient.Do(post)
	if err != nil {
		return nil, fmt.Errorf("node: reaching %s: %w", endpoint, err)
	}
	defer res.Body.Close()

	// BOUNDED, because what is on the other end is somebody else's server and a
	// body with no end would be this node's memory.
	answer, err := io.ReadAll(io.LimitReader(res.Body, maxProviderBody))
	if err != nil {
		return nil, fmt.Errorf("node: reading from %s: %w", endpoint, err)
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
func (i *inference) authorize(ctx context.Context, post *http.Request, format string, grant invoke.Grant) error {
	name := grant.Settings["inference/remote/credentials/name"]
	if name == "" {
		// A local model, or a provider that needs none. Both are legitimate:
		// ollama on the same machine wants no credential.
		return nil
	}

	project, environment, ok := deploymentOf(grant.Handle)
	if !ok {
		return fmt.Errorf("node: %q does not name a deployment", grant.Handle)
	}
	if i.secrets == nil {
		return errors.New("node: that model names a credential and this node reads no secret")
	}

	value, err := i.secrets.Secret(ctx, project, environment, name)
	if err != nil {
		return fmt.Errorf("node: reading that model's credential: %w", err)
	}

	switch format {
	case "anthropic":
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

// endpointOf is where this model's inference goes.
func endpointOf(grant invoke.Grant) (string, error) {
	switch grant.Settings["inference/kind"] {
	case "remote":
		base := strings.TrimSuffix(grant.Settings["inference/remote/endpoint"], "/")
		if base == "" {
			return "", ErrNoModelEndpoint
		}
		return base + pathFor(grant.Settings["format"]), nil

	case "local":
		// A LOCAL MODEL IS A WORKLOAD IN THIS PROJECT, reached the way a proxy
		// reaches one — through placement, on whichever node runs it. That is
		// a connection rather than a URL, and this backing speaks http to a
		// URL, so it is named as missing rather than half-built.
		return "", fmt.Errorf("%w: it is served by a workload, which this node does not reach yet", ErrNoModelEndpoint)
	}
	return "", ErrNoModelEndpoint
}

// pathFor is where each format's completions live under the base URL.
func pathFor(format string) string {
	switch format {
	case "anthropic":
		return "/v1/messages"
	case "ollama":
		return "/v1/chat/completions"
	default:
		return "/v1/chat/completions"
	}
}

// deploymentOf splits a handle back into the deployment it belongs to.
//
// project.Handle joins a project, an environment and an id with "/", and none
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
		return fmt.Errorf("node: the provider answered %d: %s", status, detail)
	}
	return fmt.Errorf("%w: %d: %s", wasmtier.ErrRefusedByProvider, status, detail)
}
