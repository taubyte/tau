package vm

import "sync"

var (
	subscribes = make(map[interface{}]chan string, 0)
	subLock    sync.RWMutex
)

func Subscribe(key interface{}) <-chan string {
	subLock.Lock()
	defer subLock.Unlock()
	s := make(chan string, 64)
	subscribes[key] = s
	return s
}

func UnSubscribe(key interface{}) {
	subLock.Lock()
	defer subLock.Unlock()
	close(subscribes[key])
	delete(subscribes, key)
}

func subsHandle(name string) {
	subLock.RLock()
	defer subLock.RUnlock()
	for _, s := range subscribes {
		select {
		case s <- name:
		default:
		}
	}
}
