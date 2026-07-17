package domainSpec

import "strings"

func (p SuffixMatcher) MatchString(s string) bool {
	return strings.HasSuffix(s, string(p))
}

func (p PrefixMatcher) MatchString(s string) bool {
	return strings.HasPrefix(s, string(p))
}

func (p MatchableDomains) MatchString(s string) bool {
	for _, p0 := range p {
		if p0.MatchString(s) {
			return true
		}
	}

	return false
}
