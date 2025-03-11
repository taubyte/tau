package auth

var (
	AllScope          = "all"
	AllowedTokenTypes = []TokenType{
		{name: "oauth", value: []byte("oauth"), length: len("oauth")},
		{name: "github", value: []byte("github"), length: len("github")},
		{name: "apikey", value: []byte("apikey"), length: len("apikey")},
	}
)
