package hoarder

import "fmt"

func CreateStashPath(cid string) string {
	return fmt.Sprintf("%s%s", StashPath, cid)
}
