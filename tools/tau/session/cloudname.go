package session

// CloudName names the cloud the session selected: the FQDN of a remote cloud
// or the universe of a Dream one. The login profile's own cloud field is not it.
func CloudName() string {
	v, _ := GetCustomCloudUrl()
	return v
}
