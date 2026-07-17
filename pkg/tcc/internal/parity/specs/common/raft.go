package common

// ServicesRequiringRaft are shapes that need config.SetRaftCluster before the service starts.
var ServicesRequiringRaft = []string{
	Patrick,
}

// RequiresRaftCluster reports whether services includes a shape listed in ServicesRequiringRaft.
func RequiresRaftCluster(services []string) bool {
	for _, s := range services {
		for _, r := range ServicesRequiringRaft {
			if s == r {
				return true
			}
		}
	}
	return false
}
