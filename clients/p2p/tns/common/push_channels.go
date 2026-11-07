package common

import "fmt"

// ChannelsForPush is every channel a push of data at path concerns. The config
// compiler pushes the whole routing index at the root path, which has no
// channel of its own, so the keys inside data must be announced too, or every
// cached route stays stale for ClientKeyCacheLifetime.
func ChannelsForPush(path []string, data interface{}) []string {
	seen := make(map[string]bool)
	var channels []string
	add := func(ch string) {
		if ch != "" && !seen[ch] {
			seen[ch] = true
			channels = append(channels, ch)
		}
	}
	for _, ch := range ChannelsFor(path...) {
		add(ch)
	}

	// a channel holds at most 4 segments, so keys below depth 5 add nothing new
	var walk func(key []string, v interface{})
	walk = func(key []string, v interface{}) {
		if len(key) > 0 {
			add(GetChannelFor(key...))
		}
		if len(key) >= 5 {
			return
		}
		for k, child := range mapEntries(v) {
			walk(append(key[:len(key):len(key)], k), child)
		}
	}
	walk(path, data)

	return channels
}

func mapEntries(v interface{}) map[string]interface{} {
	switch m := v.(type) {
	case map[string]interface{}:
		return m
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(m))
		for k, child := range m {
			out[fmt.Sprint(k)] = child
		}
		return out
	}
	return nil
}
