package p2p

func (f *Factory) generateCommandId() uint32 {
	f.commandsLock.Lock()
	defer func() {
		f.commandsIdToGrab += 1
		f.commandsLock.Unlock()
	}()
	return f.commandsIdToGrab
}

func (f *Factory) generateDiscovery(discovery [][]byte) uint32 {
	f.discoverLock.Lock()
	defer func() {
		f.discoverIdToGrab += 1
		f.discoverLock.Unlock()
	}()

	f.discover[f.discoverIdToGrab] = discovery
	return f.discoverIdToGrab
}

func (f *Factory) getDiscovery(id uint32) [][]byte {
	f.discoverLock.RLock()
	defer f.discoverLock.RUnlock()

	return f.discover[id]
}
