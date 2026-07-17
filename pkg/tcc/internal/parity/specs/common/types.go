package common

type VersioningPath struct {
	*TnsPath
}

type PathVariable string

type TnsPath struct {
	strValue string
	value    []string
}
