package common

// type Universe interface {
// 	Id() string
// 	Name() string
// 	Root() string // copy | just in case modified accidently
// 	Seer() seer.Service
// 	Auth() auth.Service
// 	Patrick() patrick.Service
// 	TNS() tns.Service
// 	Monkey() monkey.Service
// 	Hoarder() hoarder.Service
// 	Substrate() substrate.Service
// 	Context() context.Context
// 	Stop()
// 	// If no simple defined, starts one named StartAllDefaultSimple.
// 	StartAll(simples ...string) error
// 	Simple(name string) (Simple, error)
// 	StartWithConfig(mainConfig *Config) error
// 	Kill(serviceName string) error
// 	KillNodeByNameID(name string, id string) error
// 	GetPortHttp(peer.Node) (int, error)
// 	GetURLHttp(node peer.Node) (url string, err error)
// 	GetURLHttps(node peer.Node) (url string, err error)
// 	RunFixture(name string, params ...interface{}) error
// 	CreateSimpleNode(name string, config *SimpleConfig) (peer.Node, error)
// 	All() []peer.Node
// 	Register(node peer.Node, name string, ports map[string]int)
// 	Lookup(id string) (*NodeInfo, bool)
// 	Mesh(newNodes ...peer.Node)
// 	Service(name string, config *common.ServiceConfig) error
// 	Provides(services ...string) error
// 	// Calls to grab services by pid
// 	SeerByPid(pid string) (seer.Service, bool)
// 	AuthByPid(pid string) (auth.Service, bool)
// 	PatrickByPid(pid string) (patrick.Service, bool)
// 	TnsByPid(pid string) (tns.Service, bool)
// 	MonkeyByPid(pid string) (monkey.Service, bool)
// 	HoarderByPid(pid string) (hoarder.Service, bool)
// 	SubstrateByPid(pid string) (substrate.Service, bool)
// 	ListNumber(name string) int
// 	GetServicePids(name string) ([]string, error)
// 	//
// 	PortFor(string, string) int
// }

// type Simple interface {
// 	PeerNode() peer.Node
// 	CreateSeerClient(config *common.ClientConfig) error
// 	Seer() seer.Client
// 	CreateAuthClient(config *common.ClientConfig) error
// 	Auth() auth.Client
// 	CreatePatrickClient(config *common.ClientConfig) error
// 	Patrick() patrick.Client
// 	CreateTNSClient(config *common.ClientConfig) error
// 	TNS() tns.Client
// 	CreateMonkeyClient(config *common.ClientConfig) error
// 	Monkey() monkey.Client
// 	CreateHoarderClient(config *common.ClientConfig) error
// 	Hoarder() hoarder.Client
// 	Provides(clients ...string) error
// }
