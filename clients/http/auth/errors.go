package client

import "fmt"

func ErrorProviderNotSupported(provider string) error {
	return fmt.Errorf("provider `%s` is not supported", provider)
}

func ErrorBatchRequest(request string, ids []string, projectId string, err error) error {
	return fmt.Errorf("batch request `%s` for devices `%v` in project `%s` failed with: %s", request, ids, projectId, err)
}
