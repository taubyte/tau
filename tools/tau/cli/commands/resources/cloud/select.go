package cloud

import (
	"fmt"

	cliCommon "github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/clients/dream"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/validate"
	slices "github.com/taubyte/tau/utils/slices/string"

	cloudFlags "github.com/taubyte/tau/tools/tau/flags/cloud"
	cloudI18n "github.com/taubyte/tau/tools/tau/i18n/cloud"
	"github.com/urfave/cli/v2"
)

func (link) Select() cliCommon.Command {
	return cliCommon.Create(
		&cli.Command{
			Action: _select,
			Flags:  []cli.Flag{cloudFlags.FQDN, cloudFlags.Universe},
		},
	)
}

func _select(ctx *cli.Context) error {
	if ctx.NumFlags() > 2 {
		return cloudI18n.FlagError()
	}

	profile, err := loginLib.GetSelectedProfile()
	if err != nil {
		return err
	}

	switch {
	case ctx.IsSet(cloudFlags.FQDN.Name):
		profile.CloudType = common.RemoteCloud
		profile.Cloud = ctx.String(cloudFlags.FQDN.Name)

		if err := validate.SeerFQDN(ctx.Context, profile.Cloud); err != nil {
			return err
		}

		if !slices.Contains(profile.History, profile.Cloud) {
			profile.History = append(profile.History, profile.Cloud)
		}

	case ctx.IsSet(cloudFlags.Universe.Name):
		dreamClient, err := dream.Client(ctx.Context)
		if err != nil {
			return fmt.Errorf("creating dream client failed with: %w", err)
		}

		universes, err := dreamClient.Status()
		if err != nil {
			return fmt.Errorf("calling dream status failed with: %w", err)
		}

		universeName := ctx.String(cloudFlags.Universe.Name)
		_, ok := universes[universeName]
		if !ok {
			return fmt.Errorf("universe `%s` was not found", universeName)
		}

		profile.CloudType = common.DreamCloud
		profile.Cloud = universeName
	default:
		dreamClient, err := dream.Client(ctx.Context)
		if err != nil {
			return fmt.Errorf("creating dream client failed with: %w", err)
		}

		cloudSelections := []string{common.RemoteCloud}
		if _, err := dreamClient.Status(); err == nil {
			cloudSelections = append(cloudSelections, common.DreamCloud)
		}

		cloudSelections = append(cloudSelections, profile.History...)

		prev := []string{}
		if len(profile.CloudType) > 0 {
			prev = append(prev, profile.CloudType)
		}

		cloud, err := prompts.GetOrAskForSelection(ctx, "Cloud", prompts.CloudPrompts, cloudSelections, prev...)
		if err != nil {
			return err
		}
		if cloud == common.RemoteCloud {
			profile.CloudType = common.RemoteCloud
			profile.Cloud, err = prompts.GetOrRequireAString(ctx, "", prompts.FQDN, validate.FQDNValidator, profile.Cloud)
			if err != nil {
				return err
			}
			if err := validate.SeerFQDN(ctx.Context, profile.Cloud); err != nil {
				return err
			}

			if !slices.Contains(profile.History, profile.Cloud) {
				profile.History = append(profile.History, profile.Cloud)
			}

		} else if cloud == common.DreamCloud {
			profile.CloudType = common.DreamCloud
			universes, err := dreamClient.Status()
			if err != nil {
				return fmt.Errorf("calling dream status failed with: %w", err)
			}

			universeNames := make([]string, 0, len(universes))
			for name := range universes {
				universeNames = append(universeNames, name)
			}

			profile.Cloud, err = prompts.SelectInterface(universeNames, prompts.Universe, "")
			if err != nil {
				return fmt.Errorf("universe selection failed with: %w", err)
			}
		} else {
			profile.CloudType = common.RemoteCloud
			profile.Cloud = cloud
		}
	}

	config.Profiles().Set(profile.Name(), profile)
	if err := session.Set().SelectedCloud(profile.CloudType); err != nil {
		return err
	}
	if err := session.Set().CustomCloudUrl(profile.Cloud); err != nil {
		return err
	}

	cloudI18n.Success(profile.Cloud)

	return nil
}
