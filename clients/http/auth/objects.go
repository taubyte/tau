package client

type objectBasic struct {
	client *Client
}

func (o objectBasic) Client() *Client {
	return o.client
}

// CreateProjectData is the data that is sent to the server to create a new project
// The repositories only need an id that is registered with the server
type CreateProjectData struct {
	Config Repository `json:"config"`
	Code   Repository `json:"code"`
}

// Repository is the data that is sent to the server to register a repository or
// for creating a project
type Repository struct {
	Provider string `json:"provider"`
	Id       string `json:"id"`
}

// ProjectReturn is the data that is returned from the server
// when creating or getting a project
type ProjectReturn struct {
	Project *Project `json:"project"`
}

// ProjectsReturn is the data that is returned from the server when listing projects
type ProjectsReturn struct {
	Projects []*Project `json:"projects"`
}

// UserData is the data that is returned from the server when getting user data
type UserData struct {
	Company string `json:"company"`
	Email   string `json:"email"`
	Login   string `json:"login"`
	Name    string `json:"name"`
}

// Project is data which is returned from the server when getting a project
// Note: filling the RepoList field is a separate call
type Project struct {
	objectBasic
	Id       string            `json:"id"`
	Name     string            `json:"name"`
	RepoList *RawRepoDataOuter `json:"Repositories"`
}

// Device is the data that is sent to/from the server when getting or updating a device
// Note: the client and id of the Project are used in calls to the server
type Device struct {
	Project     *Project
	Id          string            `json:"id"`
	Description string            `json:"description"`
	Enabled     bool              `json:"enabled"`
	Name        string            `json:"name"`
	PrivateKey  string            `json:"privateKey"`
	PublicKey   string            `json:"publicKey"`
	Tags        []string          `json:"tags"`
	Type        string            `json:"type"`
	Env         map[string]string `json:"env"`
}

// RawRepoData is the data that is returned from the server when getting repositories
// of a project
type RawRepoData struct {
	Id       string `json:"id"` // Not exported by auth.taubyte, must use GetID()
	Name     string `json:"name"`
	Fullname string `json:"fullname"`
	Url      string `json:"url"`
}

// RawRepoDataOuter is the data that is returned from the server when
// fetching repositories of a project
type RawRepoDataOuter struct {
	Code          RawRepoData `json:"code"`
	Configuration RawRepoData `json:"configuration"`
	Provider      string      `json:"provider"`
	URL           string      `json:"url"`
}
