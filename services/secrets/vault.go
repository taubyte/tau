package secrets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// vault.go is every rule this service has, over an injected store — the same
// split services/register keeps, for the same reason: the rules are testable
// without a host, a stream or a goroutine, and there is then exactly one place
// they live.
//
// THE KEY IS THE VOCABULARY'S: "<project>/<environment>/<key>", written at the
// secret group in pkg/tcc/taubyte/v2/schema/definition.go. It is composed here
// and nowhere else, so a caller never hands this service a path.
//
// THE LAST SEGMENT IS THE SECRET'S ID, NOT THE NAME IT WAS WRITTEN AS. A
// reference compiles to an id (pkg/project/source.go, reading
// `secret: {name: …}`), so that is what a worker and a build ask with — and it
// is the better key anyway: renaming a secret in git moves the declaration and
// leaves the value exactly where it was. Turning the name a person types into
// that id is a read of the compiled project, which the CLI does and this
// service does not.

// Store is where values live. Two methods plus a listing, and the split
// between "not there" and "could not look" is the whole reason it is an
// interface rather than a map: a store that answered both with a miss would
// report a secret as unset while the swarm was simply unreachable, and the
// caller would go and set it again.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

var (
	ErrNoStore       = errors.New("secrets: a vault with no store holds nothing")
	ErrNoProject     = errors.New("secrets: no project")
	ErrNoEnvironment = errors.New("secrets: no environment")
	ErrNoName        = errors.New("secrets: no secret name")
	ErrSeparator     = errors.New(`secrets: a project, environment or secret name may not contain "/"`)
	ErrNotSet        = errors.New("secrets: nothing is set under that name")
)

const separator = "/"

// Vault reads and writes secret values under the vocabulary's key.
type Vault struct{ store Store }

// NewVault adapts a store. It is a value with no lifecycle: nothing to start,
// nothing to close, and the store it is given is owned by whoever composed it.
func NewVault(s Store) (*Vault, error) {
	if s == nil {
		return nil, ErrNoStore
	}
	return &Vault{store: s}, nil
}

// key composes the one path this service uses, refusing a segment that could
// climb out of its own scope.
//
// THE SEPARATOR CHECK IS THE ISOLATION. Without it a staging environment could
// set a secret literally named "../production/DATABASE_URL" — or, since these
// are joined rather than resolved, one named "production/DATABASE_URL" under a
// project whose environment is empty — and read across a boundary the config
// says is a boundary.
func key(project, environment, name string) (string, error) {
	switch {
	case project == "":
		return "", ErrNoProject
	case environment == "":
		return "", ErrNoEnvironment
	case name == "":
		return "", ErrNoName
	case strings.Contains(project, separator),
		strings.Contains(environment, separator),
		strings.Contains(name, separator):
		return "", fmt.Errorf("%w: %s/%s/%s", ErrSeparator, project, environment, name)
	}
	return project + separator + environment + separator + name, nil
}

// scope is the prefix List walks. It ends in the separator so that an
// environment named "prod" cannot list "production"'s secrets.
func scope(project, environment string) (string, error) {
	switch {
	case project == "":
		return "", ErrNoProject
	case environment == "":
		return "", ErrNoEnvironment
	case strings.Contains(project, separator), strings.Contains(environment, separator):
		return "", fmt.Errorf("%w: %s/%s", ErrSeparator, project, environment)
	}
	return project + separator + environment + separator, nil
}

// Get is the read on the request path.
//
// A MISS IS ErrNotSet AND NEVER AN EMPTY VALUE. A workload handed "" for its
// database password does not fail where the password is missing; it fails
// somewhere else, later, as an application that looks broken.
func (v *Vault) Get(ctx context.Context, project, environment, name string) ([]byte, error) {
	k, err := key(project, environment, name)
	if err != nil {
		return nil, err
	}
	value, found, err := v.store.Get(ctx, k)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrNotSet, k)
	}
	return value, nil
}

// Set writes a value. It is an upsert: rotating is setting again, which is the
// whole reason the value is not in git.
//
// AN EMPTY VALUE IS ALLOWED and is not the same as unset. A token that is
// legitimately the empty string is rare and refusing it would be this service
// deciding what a value means; what is refused is the MISSING one, in Get.
func (v *Vault) Set(ctx context.Context, project, environment, name string, value []byte) error {
	k, err := key(project, environment, name)
	if err != nil {
		return err
	}
	return v.store.Put(ctx, k, value)
}

// Delete removes one. Deleting what is not there succeeds — the caller wanted
// it gone and it is gone.
func (v *Vault) Delete(ctx context.Context, project, environment, name string) error {
	k, err := key(project, environment, name)
	if err != nil {
		return err
	}
	return v.store.Delete(ctx, k)
}

// List names what an environment has set, sorted.
//
// NAMES AND NEVER VALUES. This answers "is DATABASE_URL set here", which is the
// question an operator asks and the one a deploy can ask before it runs. A
// listing that carried values would make every such check an export of the
// whole environment.
func (v *Vault) List(ctx context.Context, project, environment string) ([]string, error) {
	prefix, err := scope(project, environment)
	if err != nil {
		return nil, err
	}
	keys, err := v.store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(keys))
	for _, k := range keys {
		// A kvdb lists WHOLE keys, and a datastore may hand them back with a
		// leading "/" it added itself. Cutting on the prefix rather than
		// counting segments survives both.
		_, name, ok := strings.Cut(k, prefix)
		if !ok || name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// Secret is services/worker's Secrets interface, satisfied by the vault
// itself so a node running both needs no adapter and no hop.
func (v *Vault) Secret(ctx context.Context, project, environment, secretID string) (string, error) {
	value, err := v.Get(ctx, project, environment, secretID)
	if err != nil {
		return "", err
	}
	return string(value), nil
}
