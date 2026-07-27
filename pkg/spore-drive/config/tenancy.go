package config

type TenancyParser interface {
	Provider() string
	Owner() string

	SetProvider(string) error
	SetOwner(string) error

	App() TenancyAppParser
}

// TenancyAppParser holds the credential the owning namespace grants the cloud.
// Key is the PEM itself, not a path: it travels inline in the shape config the
// same way privatekey and swarmkey already do.
type TenancyAppParser interface {
	ClientId() string
	Key() string

	SetClientId(string) error
	SetKey(string) error
}

type (
	tenancy    leaf
	tenancyApp leaf
)

func (t *tenancy) Provider() (v string) {
	t.Query.Get("provider").Value(&v)
	return
}

func (t *tenancy) Owner() (v string) {
	t.Query.Get("owner").Value(&v)
	return
}

func (t *tenancy) SetProvider(v string) error {
	return t.Query.Get("provider").Set(v).Commit()
}

func (t *tenancy) SetOwner(v string) error {
	return t.Query.Get("owner").Set(v).Commit()
}

func (t *tenancy) App() TenancyAppParser {
	return &tenancyApp{root: t.root, Query: t.Query.Get("app")}
}

func (a *tenancyApp) ClientId() (v string) { a.Query.Get("client-id").Value(&v); return }
func (a *tenancyApp) Key() (v string)      { a.Query.Get("key").Value(&v); return }

func (a *tenancyApp) SetClientId(v string) error {
	return a.Query.Get("client-id").Set(v).Commit()
}

func (a *tenancyApp) SetKey(v string) error { return a.Query.Get("key").Set(v).Commit() }
