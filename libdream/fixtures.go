package libdream

func init() {
	fixtures = make(map[string]FixtureHandler)
}

// Register a fixture
func RegisterFixture(name string, handler FixtureHandler) {
	fixturesLock.Lock()
	defer fixturesLock.Unlock()
	fixtures[name] = handler
}

func ValidFixtures() []string {
	keys := make([]string, 0, len(FixtureMap))
	for k := range FixtureMap {
		keys = append(keys, k)
	}

	return keys
}
