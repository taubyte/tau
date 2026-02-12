package constants

// Session storage keys (seer-backed). Lowercase; not env vars.
const (
	KeyProfile        = "profile"
	KeyProject        = "project"
	KeyApplication    = "application"
	KeySelectedCloud  = "selected_cloud"   // "remote" | "dream"
	KeyCustomCloudURL = "custom_cloud_url" // cloud value: FQDN when remote, universe name when dream
)
