package prompts

import (
	"fmt"
	"strings"

	domainSchema "github.com/taubyte/tau/pkg/schema/domains"
	"github.com/taubyte/tau/pkg/schema/project"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/env"
	"github.com/taubyte/tau/tools/tau/flags"
	domainFlags "github.com/taubyte/tau/tools/tau/flags/domain"
	domainLib "github.com/taubyte/tau/tools/tau/lib/domain"

	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/utils/id"
	"github.com/urfave/cli/v2"
)

// NeedGenerateDomain checks for available domains if none found asks the user if they would like to generate one; returns an error
func NeedGenerateDomain(ctx *cli.Context) error {
	var project project.Project
	project, err := projectLib.SelectedProjectInterface()
	if err != nil {
		return err
	}

	selectedApp, _ := env.GetSelectedApplication()
	local, global := project.Get().Domains(selectedApp)
	if len(local)+len(global) == 0 {
		if GetOrAskForBoolDefaultTrue(ctx, domainFlags.Generated.Name, NoDomainGeneratePrompt) {
			fqdn, err := domainLib.NewGeneratedFQDN("")
			if err != nil {
				return err
			}

			_, err = domainLib.Set(&structureSpec.Domain{
				Id:   id.Generate(project.Get().Id(), fqdn),
				Name: common.DefaultGeneratedDomainName,
				Fqdn: fqdn,
			})
			if err != nil {
				return err
			}

			return ctx.Set(flags.Domains.Name, common.DefaultGeneratedDomainName)
		}

		return ErrorNoValidDomains
	}

	return nil
}

func generateDomainOption(domain domainSchema.Domain) string {
	getter := domain.Get()

	app := getter.Application()
	if len(app) > 0 {
		return fmt.Sprintf("%s/%s ( %s )", app, getter.Name(), getter.FQDN())
	}

	return fmt.Sprintf("%s ( %s )", getter.Name(), getter.FQDN())
}

/*
	buildDomainOptions takes domains from the domains flag and previously selected

Parameters:

	flagDomainsLowerCase: the domain names or FQDNs parsed from the domains flag
	prev: previously selected domain names

Returns:

	flagDomains: selected domains based on domains provided
	previous: the previously selected domain options
	options: possible selections
	optionMap: maps the selected options back to the domain name
	err: an error
*/
func buildDomainOptions(flagDomainsLowerCase []string, prev ...string) (flagDomains, previous, options []string, optionMap map[string]string, err error) {
	var project project.Project
	project, err = projectLib.SelectedProjectInterface()
	if err != nil {
		return
	}

	selectedApp, _ := env.GetSelectedApplication()
	local, global := project.Get().Domains(selectedApp)

	// Build options and find potential selections
	flagDomains = make([]string, 0)
	previous = make([]string, 0)
	options = make([]string, 0)

	// Maps the option (name/fqdn) back to the name of the domain
	optionMap = make(map[string]string)

	generator := func(name string, domain domainSchema.Domain) {
		option := generateDomainOption(domain)
		options = append(options, option)
		optionMap[option] = name

		for _, _prev := range prev {
			if name == _prev {
				previous = append(previous, option)
			}
		}

		// Build selection from flag
		if len(flagDomainsLowerCase) > 0 {
			nameLC := strings.ToLower(name)
			fqdnLC := strings.ToLower(domain.Get().FQDN())
			for _, flagDomain := range flagDomainsLowerCase {
				if flagDomain == nameLC || flagDomain == fqdnLC {
					flagDomains = append(flagDomains, name)
				}
			}
		}
	}

	var domain domainSchema.Domain
	for _, name := range local {
		domain, err = project.Domain(name, selectedApp)
		if err != nil {
			return
		}

		generator(name, domain)
	}

	for _, name := range global {
		domain, err = project.Domain(name, "")
		if err != nil {
			return
		}

		generator(name, domain)
	}

	return
}

func GetOrSelectDomainsWithFQDN(ctx *cli.Context, prev ...string) ([]string, error) {
	err := NeedGenerateDomain(ctx)
	if err != nil {
		return nil, err
	}

	// Names or FQDNs
	flagDomains := ctx.StringSlice(flags.Domains.Name)

	// Lowercase flagDomains for simple comparison
	var flagDomainsLC []string
	if len(flagDomains) > 0 {
		flagDomainsLC = make([]string, len(flagDomains))
		for idx, domain := range flagDomains {
			flagDomainsLC[idx] = strings.ToLower(domain)
		}
	}

	domains, selected, options, optionMap, err := buildDomainOptions(flagDomainsLC, prev...)
	if err != nil {
		return nil, err
	}

	// Simply return if we get domains from the flag
	if len(domains) > 0 {
		// TODO, confirm len(flagDomains) == len(domains) and warn that given flags were invalid
		// possibly offering another selection

		return domains, nil
	}

	var selectedDomains []string
	multiselectPrompt(&selectedDomains, MultiSelectConfig{
		Prompt:   Domains,
		Options:  options,
		Previous: selected,
		Required: true,
	})

	domains = make([]string, len(selectedDomains))
	for idx, domain := range selectedDomains {
		domains[idx] = optionMap[domain]
	}

	return domains, nil
}
