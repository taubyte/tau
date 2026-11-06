package worker

import (
	"context"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Subscriber is one workload that declared it subscribes to one stream.
type Subscriber struct {
	// Target is the workload to invoke, in the form Invoke takes.
	Target Target

	// ID is the subscriber's own resource id. With Target it names what a log
	// remembers this subscriber's place under.
	ID string

	// Stream is the grant the subscriber's own access block produced: its label,
	// the handle, the engine's settings and the refusal if this cloud will not
	// serve it.
	Stream invoke.Grant

	// Path is where a message is posted.
	Path string
}

// subscribeOp is the vocabulary's own verb for the delivered half of a stream.
const subscribeOp = "subscribe"

// Subscribers is every stream subscriber in the deployments this node holds, in no
// particular order.
func (s *Service) Subscribers(ctx context.Context) []Subscriber {
	var out []Subscriber
	for _, at := range s.projects.Deployments() {
		held, err := s.subscribed(ctx, at)
		if err != nil {
			continue
		}
		for _, subs := range held.byHandle {
			out = append(out, subs...)
		}
	}
	return out
}

// SubscribersOf is every subscriber of the stream a handle names, in its
// deployment as this node holds it now. The slice is shared; callers do not
// change it.
func (s *Service) SubscribersOf(ctx context.Context, handle string) ([]Subscriber, error) {
	project, environment, _ := specs.SplitHandle(handle)
	held, err := s.subscribed(ctx, register.Deployment{Project: project, Environment: environment})
	if err != nil {
		return nil, err
	}
	return held.byHandle[handle], nil
}

// subscribed is a deployment's subscribers under its current object, worked out
// once per object: a publish asks on every message.
func (s *Service) subscribed(ctx context.Context, at register.Deployment) (*subscriptions, error) {
	proj, pointer, err := s.projects.At(ctx, at)
	if err != nil {
		return nil, err
	}
	if held, ok := s.subscriptions.Load(at); ok && held.(*subscriptions).object == pointer.Object {
		return held.(*subscriptions), nil
	}

	next := &subscriptions{object: pointer.Object, byHandle: map[string][]Subscriber{}}
	eachScope(proj, nil, func(scope []string) {
		for _, sub := range s.subscribersIn(ctx, proj, at, scope) {
			next.byHandle[sub.Stream.Handle] = append(next.byHandle[sub.Stream.Handle], sub)
		}
	})
	s.subscriptions.Store(at, next)
	return next, nil
}

// subscriptions is one deployment's subscribers by the handle of the stream they
// subscribe to, under one object.
type subscriptions struct {
	object   register.ObjectID
	byHandle map[string][]Subscriber
}

// subscribersIn is every subscriber declared in one scope.
func (s *Service) subscribersIn(ctx context.Context, proj *specs.Project, at register.Deployment, scope []string) []Subscriber {
	kinds, err := proj.Kinds(scope)
	if err != nil {
		return nil
	}

	var out []Subscriber
	for _, kind := range kinds {
		if len(schema.EnginesOf(kind)) == 0 {
			continue
		}
		names, err := proj.Names(scope, kind)
		if err != nil {
			continue
		}
		for _, name := range names {
			out = append(out, s.subscribersOf(ctx, proj, at, scope, kind, name)...)
		}
	}
	return out
}

// subscribersOf is one resource's subscribe entries, resolved.
func (s *Service) subscribersOf(ctx context.Context, proj *specs.Project, at register.Deployment, scope []string, kind, name string) []Subscriber {
	resource, id, err := proj.Resource(scope, kind, name)
	if err != nil {
		return nil
	}

	wanted := map[string]specs.Reach{}
	for _, reach := range resource.Reaches(streamKind) {
		if contains(reach.Ops, subscribeOp) {
			wanted[reach.Label] = reach
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	target := Target{
		Project: at.Project, Environment: at.Environment,
		Scope: scope, Kind: kind, Name: name,
	}
	spec, err := s.Resolve(ctx, target)
	if err != nil {
		return nil
	}

	var out []Subscriber
	for _, grant := range spec.Reaches {
		reach, ok := wanted[grant.Label]
		if grant.Kind != streamKind || !ok {
			continue
		}
		out = append(out, Subscriber{
			Target: target,
			ID:     id,
			Stream: grant,
			Path:   deliverPath(reach.DeliverPath),
		})
	}
	return out
}

// streamSettings is what a publisher or a consumer needs to reach the topic: the
// engine, and on a log its name, address, tls and how to authenticate. The
// credential stays a reference, resolved by the vault on connection.
func streamSettings(target specs.Resource) map[string]string {
	stream := specs.ReadStream(target)
	out := map[string]string{}
	put := func(key, value string) {
		if value != "" {
			out[key] = value
		}
	}
	put(specs.StreamKeyEngine, stream.Engine)
	put(specs.StreamKeyName, stream.Name)
	put(specs.StreamKeyHost, stream.Host)
	put(specs.StreamKeyTls, stream.Tls)
	put(specs.StreamKeySasl, stream.Sasl)
	put(specs.StreamCredentialsKeyName, stream.Credentials.Name)
	put(specs.StreamCredentialsKeyNamespace, stream.Credentials.Namespace)
	return out
}
