package mocks

func NewPlugin(failInstance bool) MockedPlugin {
	return &mockPlugin{InstanceFail: failInstance}
}

func NewPluginInstance() MockedPluginInstance {
	return &mockPluginInstance{}
}

func NewModuleInstance() MockedModuleInstance {
	return &mockModuleInstance{}
}

func NewFunctionInstance() MockedFunctionInstance {
	return &mockFunctionInstance{}
}

func NewReturn() MockedReturn {
	return &mockReturn{}
}
