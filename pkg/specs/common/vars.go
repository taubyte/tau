package common

const (
	ProjectPathVariable     PathVariable = "projects"
	ApplicationPathVariable PathVariable = "applications"
	ProjectIndexVariable    PathVariable = "project"

	/********************** Versioning VARS **********************/

	LinksPathVariable         PathVariable = "links"
	BranchPathVariable        PathVariable = "branches"
	CommitPathVariable        PathVariable = "commit"
	CurrentCommitPathVariable PathVariable = "current"
)

// TODO remove this and iterate, default branch should be gathered from a given repository
var DefaultBranches = []string{"main", "master"}

const (
	Auth      = "auth"
	Patrick   = "patrick"
	Monkey    = "monkey"
	TNS       = "tns"
	Hoarder   = "hoarder"
	Substrate = "substrate"
	Seer      = "seer"
	Gateway   = "gateway"
)

var (
	Services          = []string{Auth, Patrick, Monkey, TNS, Hoarder, Substrate, Seer, Gateway}
	HTTPServices      = []string{Patrick, Substrate, Seer, Auth, Gateway}
	P2PStreamServices = []string{Seer, Auth, Patrick, TNS, Monkey, Hoarder, Substrate}
)
