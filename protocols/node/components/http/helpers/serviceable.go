package helpers

func ServiceId(projectId, host, resourceId string) string {
	return "." + projectId[:8] + "." + host + "." + resourceId
}
