package vm

type Service interface {
	New(context Context, config Config) (Instance, error)
	Source() Source
	Close() error
}
