package loginPrompts

// English
const (
	githubLoginListenPort = "1442"

	UseAsDefault      = "Use this as default:"
	SelectAProfile    = "Select a Profile:"
	ProfileName       = "Profile Name:"
	TokenPrompt       = "Token:"
	GitProviderPrompt = "Git Provider:"

	GetTokenWith       = "Get token with:"
	GetTokenFromWeb    = "Login with %s"
	EnterTokenManually = "Enter token manually"

	NoSessionProvided          = "no session provided"
	ListenOnPortFailedWith     = "listen on %s failed with: %s"
	ShuttingDownHttpFailedWith = "shutting down http server on %s failed with: %s"
	StartingHttpFailedWith     = "starting http server on %s failed with: %s"

	SuccessCheckBackAtYourTerminal = "Success, you can close this page!"

	OpenURLToLogin = "Open to login with %s: %s"
)

var (
	DefaultProvider = "github"
	Providers       = []string{"github"}
)
