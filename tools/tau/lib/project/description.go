package projectLib

import client "github.com/taubyte/tau/clients/http/auth"

func Description(p *client.Project) string {
	config, err := p.Config()
	if err != nil {
		return ""
	}

	return config.Description
}
