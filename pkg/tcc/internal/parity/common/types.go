package common

type RepositoryType int

const (
	UnknownRepository RepositoryType = iota
	ConfigRepository
	CodeRepository
	LibraryRepository
	WebsiteRepository
)
