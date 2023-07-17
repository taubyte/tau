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
