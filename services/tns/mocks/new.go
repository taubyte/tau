package mocks

func New() MockedTns {
	return &mockTns{
		mapDef: make(map[string]interface{}, 0),
	}
}
