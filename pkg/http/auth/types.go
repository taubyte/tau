package auth

type TokenType struct {
	name   string
	value  []byte
	length int
}

type Authorization struct {
	Type  string
	Token string
	Scope []string
}
