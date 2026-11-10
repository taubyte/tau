package common

import "strings"

func GetChannelFor(key ...string) string {
	switch len(key) {
	case 0:
		return ""
	case 1:
		return "/tns/updates/" + key[0]
	default:
		max := 4
		if max > len(key) {
			max = len(key) - 1
		}
		return "/tns/updates/" + strings.Join(key[0:max], "/")
	}
}

// ChannelsFor is every channel a change to key concerns: its own and those a
// cached fetch of any of its ancestors (two segments or more) listens on.
func ChannelsFor(key ...string) []string {
	seen := make(map[string]bool)
	var channels []string
	for n := len(key); n >= 2; n-- {
		if ch := GetChannelFor(key[:n]...); ch != "" && !seen[ch] {
			seen[ch] = true
			channels = append(channels, ch)
		}
	}
	return channels
}
