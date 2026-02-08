package app

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/pkg/config"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"

	seer "github.com/taubyte/tau/pkg/yaseer"
)

// Parse from yaml
func parseSourceConfig(ctx *cli.Context, shape string) (string, config.Config, *config.Source, error) {
	root := ctx.Path("root")
	if root == "" {
		root = config.DefaultRoot
	}

	if !filepath.IsAbs(root) {
		return "", nil, nil, fmt.Errorf("root folder `%s` is not absolute", root)
	}

	configRoot := root + "/config"
	configPath := ctx.Path("path")
	if configPath == "" {
		configPath = path.Join(configRoot, shape+".yaml")
	}

	err := configMigration(seer.SystemFS(configRoot), shape)
	if err != nil {
		return "", nil, nil, fmt.Errorf("migration of `%s` failed with: %w", configPath, err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", nil, nil, fmt.Errorf("reading config file path `%s` failed with: %w", configPath, err)
	}

	src := &config.Source{}

	if err = yaml.Unmarshal(data, &src); err != nil {
		return "", nil, nil, fmt.Errorf("yaml unmarshal failed with: %w", err)
	}

	cnf, err := config.New(config.WithSource(src, config.SourceOptions{
		Root:    root,
		Shape:   shape,
		DevMode: ctx.Bool("dev-mode"),
	}))
	if err != nil {
		return "", nil, nil, fmt.Errorf("config: %w", err)
	}

	pkey, err := crypto.UnmarshalPrivateKey(cnf.PrivateKey())
	if err != nil {
		return "", nil, nil, err
	}
	pid, err := peer.IDFromPublicKey(pkey.GetPublic())
	if err != nil {
		return "", nil, nil, err
	}

	return pid.String(), cnf, src, nil
}
