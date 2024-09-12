package domainLib

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	client "github.com/taubyte/tau/clients/http/auth"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/env"
	domainI18n "github.com/taubyte/tau/tools/tau/i18n/domain"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	authClient "github.com/taubyte/tau/tools/tau/singletons/auth_client"
)

type validator struct {
	getter
}

func NewValidator(name string) (Validator, error) {
	info, err := get(name)
	if err != nil {
		return nil, err
	}

	return &validator{info}, nil
}

// Internal does not require info
// TODO info should be global
func newValidator(info getter) Validator {
	return &validator{info}
}

func (r *validator) ValidateFQDN(fqdn string) (response client.DomainResponse, err error) {
	client, err := authClient.Load()
	if err != nil {
		return
	}

	return client.RegisterDomain(fqdn, r.project.Get().Id())
}

func NewGeneratedFQDN(prefix string) (string, error) {
	project, err := projectLib.SelectedProjectInterface()
	if err != nil {
		return "", err
	}

	// Get last eight characters of project id for use in fqdn
	projectID := project.Get().Id()
	if len(projectID) < 8 {
		return "", domainI18n.InvalidProjectIDEight(projectID)
	}
	projectID = strings.ToLower(projectID[len(projectID)-8:])

	parseFqdn := func(suffix string) string {
		return fmt.Sprintf("%s%d%s", projectID, ProjectDomainCount(project), suffix)
	}

	// Generate fqdn
	var fqdn string
	selectedNetwork, _ := env.GetSelectedNetwork()
	switch selectedNetwork {
	case common.DreamlandNetwork:
		universe, _ := env.GetCustomNetworkUrl()
		fqdn = parseFqdn(fmt.Sprintf(".%s.localtau", universe))
	case common.PythonTestNetwork:
		fqdn = parseFqdn(DefaultGeneratedFqdnSuffix)
	case common.RemoteNetwork:
		customNetworkUrl, _ := env.GetCustomNetworkUrl()
		customGeneratedFqdn, err := FetchCustomNetworkGeneratedFqdn(customNetworkUrl)
		if err != nil {
			return "", err
		}

		fqdn = parseFqdn(customGeneratedFqdn)
	}

	// Attach prefix
	if len(prefix) > 0 {
		fqdn = fmt.Sprintf("%s-%s", prefix, fqdn)
	}

	return fqdn, nil
}

func IsAGeneratedFQDN(fqdn string) (bool, error) {
	selectedNetwork, _ := env.GetSelectedNetwork()
	switch selectedNetwork {
	case common.DreamlandNetwork:
		universe, _ := env.GetCustomNetworkUrl()
		return strings.HasSuffix(fqdn, fmt.Sprintf(".%s.localtau", universe)), nil
	case common.PythonTestNetwork:
		return strings.HasSuffix(fqdn, DefaultGeneratedFqdnSuffix), nil
	case common.RemoteNetwork:
		customNetworkUrl, _ := env.GetCustomNetworkUrl()
		customGeneratedFqdn, err := FetchCustomNetworkGeneratedFqdn(customNetworkUrl)
		if err != nil {
			return false, err
		}

		return strings.HasSuffix(fqdn, customGeneratedFqdn), nil
	default:
		return false, fmt.Errorf("%s is not a valid network type", selectedNetwork)
	}
}

// TODO: Move to specs
func FetchCustomNetworkGeneratedFqdn(fqdn string) (string, error) {
	resp, err := http.Get(fmt.Sprintf("https://seer.tau.%s/network/config", fqdn))
	if err != nil {
		return "", fmt.Errorf("fetching generated url prefix for fqdn `%s` failed with: %s", fqdn, err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response failed with: %s", err)
	}

	bodyStr := strings.Trim(string(body), "\"")

	return formatGeneratedSuffix(bodyStr), nil
}

func formatGeneratedSuffix(suffix string) string {
	if !strings.HasPrefix(suffix, ".") {
		suffix = "." + suffix
	}

	return suffix
}
