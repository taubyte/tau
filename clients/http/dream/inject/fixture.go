package inject

import "fmt"

func Fixture(name string, params interface{}) Injectable {
	return Injectable{
		Name: name,
		Run: func(universe string) string {
			return fmt.Sprintf("/fixture/%s/%s", universe, name)
		},
		Params: params,
		Method: POST,
	}
}
