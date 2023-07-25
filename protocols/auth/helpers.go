package auth

import (
	"strings"
)

func generateWildCardDomain(fqdn string) string {
	split := strings.SplitAfterN(fqdn, ".", 2)
	split[0] = "*"
	hostName := strings.Join(split, ".")
	return hostName
}

func getMapValues(m map[string]interface{}) []interface{} {
	vl := make([]interface{}, len(m))
	var i = 0
	for _, v := range m {
		vl[i] = v
		i = i + 1
	}
	return vl
}

func extractIdFromKey(list []string, split string, index int) []string {
	ids := make([]string, 0)
	unique := make(map[string]bool)
	for _, id := range list {
		list := strings.Split(id, split)
		if len(list) > 1 {
			if _, ok := unique[list[index]]; !ok {
				unique[list[index]] = true
				ids = append(ids, list[index])
			}
		}
	}
	return ids
}
