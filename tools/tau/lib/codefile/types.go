package codefile

type CodePath string

func (p CodePath) String() string {
	return string(p)
}
