package prompts

import (
	"fmt"
	"strings"

	"github.com/pterm/pterm"
	"github.com/urfave/cli/v2"
)

// Unused, was used in devices
func GetOrAskForEnvironmentVars(c *cli.Context, prev ...map[string]string) map[string]string {
	// TODO should these be unique?
	flag := c.String("env-vars")
	envVars := make(map[string]string)
	if flag == "" {
		if len(prev) != 0 {
			for _key, _value := range prev[0] {
				key := GetOrAskForAStringValue(c, "env-key", "Environment key (enter for no value)", _key)
				if key == "" {
					continue
				}
				value := GetOrAskForAStringValue(c, "env-value", fmt.Sprintf("Environment value for %s", key), _value)
				envVars[key] = value
			}
		}

		for {
			key := GetOrAskForAStringValue(c, "env-key", "Environment key (enter for no value)")
			if key == "" {
				break
			}
			value := GetOrAskForAStringValue(c, "env-value", fmt.Sprintf("Environment value for %s", key))
			envVars[key] = value
		}

	} else {
		envVars = parseEnvVars(flag)
	}
	return envVars
}

func parseEnvVars(envVars string) map[string]string {
	envVarsMap := make(map[string]string)
	for _, envVar := range strings.Split(envVars, ",") {
		envVar = strings.TrimSpace(envVar)
		if envVar == "" {
			continue
		}
		var parts []string
		if strings.Contains(envVar, "=") {
			parts = strings.Split(envVar, "=")
		} else if strings.Contains(envVar, ":") {
			parts = strings.Split(envVar, ":")
		} else {
			pterm.Warning.Printf("Invalid pair `%s` write as `key=value` or `key:value`\n", envVar)
		}

		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		envVarsMap[key] = value
	}
	return envVarsMap
}
