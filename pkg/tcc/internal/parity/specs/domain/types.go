package domainSpec

type tnsHelper struct{}

type DomainMatcher interface {
	MatchString(string) bool
}

type SuffixMatcher string
type PrefixMatcher string
type MatchableDomains []DomainMatcher
