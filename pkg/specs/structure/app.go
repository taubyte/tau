package structureSpec

// App is hand-written (unlike the 9 resource structs, which tcc-gen generates
// from the DSL). It's the applications *container* identity — plain common
// fields, no object-addressing methods and no pkg/specs helper package — and it
// lives at the schema Root rather than in the resource list, outside the
// generator's resource walk. Not a config-decode surface, so no divergence risk.
type App struct {
	Id          string
	Name        string
	Description string
	Tags        []string
}
