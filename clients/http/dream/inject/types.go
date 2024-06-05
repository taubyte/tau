package inject

type Injectable struct {
	Name   string
	Run    func(universe string) (url string)
	Method Method
	Params interface{}
	Config interface{}
}

type Method int

const (
	GET Method = iota
	POST
	DELETE
)

func (m Method) String() string {
	switch m {
	case GET:
		return "GET"
	case POST:
		return "POST"
	case DELETE:
		return "DELETE"
	default:
		return "Unknown"
	}
}
