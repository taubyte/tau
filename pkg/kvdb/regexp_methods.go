package kvdb

import (
	"context"
	"regexp"

	query "github.com/ipfs/go-datastore/query"
)

type FilterKeyRegEx struct {
	re []*regexp.Regexp
}

func (f *FilterKeyRegEx) Filter(e query.Entry) bool {
	for _, r := range f.re {
		if r == nil {
			continue
		}
		if r.MatchString(e.Key) {
			return true
		}
	}

	return false
}

func NewFilterKeyRegEx(regexs ...string) (*FilterKeyRegEx, error) {
	f := &FilterKeyRegEx{
		re: make([]*regexp.Regexp, 0, len(regexs)),
	}

	for _, rs := range regexs {
		re, err := regexp.Compile(rs)
		if err != nil {
			return nil, err
		}
		f.re = append(f.re, re)
	}

	return f, nil
}

func (kvd *kvDatabase) ListRegEx(ctx context.Context, prefix string, regexs ...string) ([]string, error) {
	result, err := kvd.listRegEx(ctx, prefix, regexs...)
	if err != nil {
		return nil, err
	}
	return keysOf(ctx, result)
}

func (kvd *kvDatabase) listRegEx(ctx context.Context, prefix string, regexs ...string) (query.Results, error) {
	filter, err := NewFilterKeyRegEx(regexs...)
	if err != nil {
		return nil, err
	}

	return kvd.datastore.Query(ctx, query.Query{
		Prefix:   prefix,
		Filters:  []query.Filter{filter},
		KeysOnly: true,
	})
}
