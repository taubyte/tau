package auth

const (
	deployKeyName    = "taubyte_deploy_key"
	devDeployKeyName = "taubyte_deploy_key_dev"
)

// deployKeyTitle names the deploy keys this cloud adds. Registering a
// repository removes its other keys of the same title, so each cloud needs its
// own: two clouds building one repository must not delete each other's key.
// Dream keeps one title for all its universes, being one machine's cloud.
func deployKeyTitle(devMode bool, fqdn string) string {
	if devMode {
		return devDeployKeyName
	}
	if fqdn == "" {
		return deployKeyName
	}
	return deployKeyName + "_" + fqdn
}
