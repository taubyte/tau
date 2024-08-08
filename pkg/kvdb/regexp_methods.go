package kvdb

import (
	"context"
	"fmt"
	"regexp"
	"time"

	query "github.com/ipfs/go-datastore/query"
)

type FilterKeyRegEx struct {
	re []*regexp.Regexp
}

func (f *FilterKeyRegEx) Filter(e query.Entry) bool {
	for _, r := range f.re {
		if r == nil {
			panic(fmt.Sprintf("Query filter got a Nil regexp %v", f))
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

	all_result, err := result.Rest()
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0)
	for _, entry := range all_result {
		keys = append(keys, entry.Key)
	}

	return keys, nil
}

func (kvd *kvDatabase) ListRegExAsync(ctx context.Context, prefix string, regexs ...string) (chan string, error) {
	result, err := kvd.listRegEx(ctx, prefix, regexs...)
	if err != nil {
		return nil, err
	}

	c := make(chan string, QueryBufferSize)
	go func() {
		defer close(c)
		defer result.Close()
		source := result.Next()
		for {
			select {
			case <-ctx.Done():
				return
			case entry, ok := <-source:
				if !ok || entry.Error != nil {
					return
				}

				c <- entry.Key
			case <-time.After(ReadQueryResultTimeout):
				return
			}
		}
	}()

	return c, nil
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
