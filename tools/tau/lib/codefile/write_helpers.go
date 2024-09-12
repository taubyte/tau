//go:build !windows

package codefile

import "path"

func getTemplateCommon(split []string) string {
	return path.Join("/", path.Join(split[0:len(split)-1]...), "common")
}
