package database

import (
	"context"
	"fmt"
	"regexp"
	"time"

	query "github.com/ipfs/go-datastore/query"
)

/*
	type RegExpCacheEntry struct {
		score int
		re    *regexp.Regexp
	}

var RegexpCache map[string]RegExpCacheEntry
var RegexpCacheMaxSize = 64
var RegexpCacheTrim = 8
var RegexpCacheLock sync.Mutex

type RegExpCacheEntryList []RegExpCacheEntry

func (p RegExpCacheEntryList) Len() int           { return len(p) }
func (p RegExpCacheEntryList) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p RegExpCacheEntryList) Less(i, j int) bool { return p[i].score < p[j].score }

	func (p RegExpCacheEntryList) FromCache(except ...string) {
		//p = make(RegExpCacheEntryList, 0)
		for k, v := range RegexpCache {
			skip := false
			for _, ex := range except {
				if k == ex {
					skip = true
					break
				}
			}
			if skip == true {
				continue
			}
			p = append(p, v)
		}
	}

	func (p RegExpCacheEntryList) updateCache() {
		RegexpCache = make(map[string]RegExpCacheEntry, len(p))
		for _, v := range p {
			RegexpCache[v.re.String()] = v
		}
	}

	func _compileRegexp(s string) (*regexp.Regexp, error) {
		if rent, ok := RegexpCache[s]; ok == true {
			rent.score++
			RegexpCache[s] = rent
			return rent.re, nil
		}
		re, err := regexp.Compile(s)
		if err != nil {
			RegexpCache[s] = RegExpCacheEntry{
				score: 1,
				re:    re,
			}
			return re, err
		}
		return nil, err
	}

	func compileRegexp(s string) (*regexp.Regexp, error) {
		RegexpCacheLock.Lock()
		defer RegexpCacheLock.Unlock()
		re, err := _compileRegexp(s)
		if err != nil {
			return nil, err
		}
		if len(RegexpCache) > RegexpCacheMaxSize {
			p := make(RegExpCacheEntryList, 0)
			p.FromCache(s)
			sort.Sort(p)
			p[RegexpCacheTrim:].updateCache()
		}
		return re, nil
	}
*/
type FilterKeyRegEx struct {
	re []*regexp.Regexp
}

func (f *FilterKeyRegEx) Filter(e query.Entry) bool {
	for _, r := range f.re {
		if r == nil {
			panic(fmt.Sprintf("Query filter got a Nil regexp %v", f))
		}
		if r.MatchString(e.Key) == true {
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
		re, err := regexp.Compile(rs) // compileRegexp(rs)
		if err != nil {
			return nil, err
		}
		f.re = append(f.re, re)
	}

	return f, nil
}

func (kvd *KVDatabase) ListRegEx(ctx context.Context, prefix string, regexs ...string) ([]string, error) {

	result, err := kvd.listRegEx(ctx, prefix, regexs...)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0)
	all_result, err := result.Rest()
	if err != nil {
		return nil, err
	}
	for _, entry := range all_result {
		keys = append(keys, entry.Key)
	}
	return keys, nil
}

func (kvd *KVDatabase) ListRegExAsync(ctx context.Context, prefix string, regexs ...string) (chan string, error) {

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
			case entry, ok := <-source:
				if ok == false {
					return
				}
				if entry.Error != nil {
					return
				}
				c <- entry.Key
				/*select {
				case c <- entry.Key:
				default:
				}*/
			case <-ctx.Done():
				return
			case <-time.After(ReadQueryResultTimeout):
				return
			}
		}
	}()

	return c, nil
}

func (kvd *KVDatabase) listRegEx(ctx context.Context, prefix string, regexs ...string) (query.Results, error) {
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
