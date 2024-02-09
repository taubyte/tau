package app

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/taubyte/tau/config"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v2"
)

func exportConfig(ctx *cli.Context) error {
	root := ctx.Path("root")
	if root == "" {
		root = config.DefaultRoot
	}

	if !filepath.IsAbs(root) {
		return fmt.Errorf("root folder `%s` is not absolute", root)
	}

	shape := ctx.String("shape")

	configRoot := root + "/config"
	configPath := ctx.Path("path")
	if configPath == "" {
		configPath = path.Join(configRoot, shape+".yaml")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("shape %s does not exist", shape)
	}

	host, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("faile to fetch hostname with %w", err)
	}

	var version *string
	if v := ctx.String("version"); v != "" {
		version = &v
	}

	bundle := &config.Bundle{
		Origin: config.BundleOrigin{
			Shape:    shape,
			Host:     host,
			Creation: time.Now(),
			Version:  version,
		},
	}

	if err = yaml.Unmarshal(data, &(bundle.Source)); err != nil {
		return fmt.Errorf("yaml unmarshal failed with: %w", err)
	}

	pkey := bundle.Privatekey
	if !ctx.Bool("unsafe") {
		pkey = ""
	}

	skfilename := path.Join(configRoot, bundle.Swarmkey)
	skdata, err := os.ReadFile(skfilename)
	if err != nil {
		return fmt.Errorf("reading %s failed with: %w %#v", skfilename, err, bundle)
	}

	dvsfilename := path.Join(configRoot, bundle.Domains.Key.Private)
	dvsdata, err := os.ReadFile(dvsfilename)
	if err != nil {
		return fmt.Errorf("reading %s failed with: %w", dvsfilename, err)
	}

	var dvpdata []byte
	if bundle.Domains.Key.Public != "" {
		dvpfilename := path.Join(configRoot, bundle.Domains.Key.Public)
		dvpdata, err = os.ReadFile(dvpfilename)
		if err != nil {
			return fmt.Errorf("reading %s failed with: %w", dvsfilename, err)
		}
	}

	if ctx.Bool("protect") {
		bundle.Origin.Protected = true
		if passwd, err := promptPassword("Password?"); err != nil {
			return fmt.Errorf("faild to read password with %w", err)
		} else {
			if skdata, err = encrypt(skdata, passwd); err != nil {
				return fmt.Errorf("faild to encrypt swarm key with %w", err)
			}
			if dvsdata, err = encrypt(dvsdata, passwd); err != nil {
				return fmt.Errorf("faild to encrypt domain's private key key with %w", err)
			}
			if dvpdata != nil {
				if dvpdata, err = encrypt(dvpdata, passwd); err != nil {
					return fmt.Errorf("faild to encrypt domain's public key key with %w", err)
				}
			}
			if pkey != "" {
				pkdata, err := encrypt([]byte(pkey), passwd)
				if err != nil {
					return fmt.Errorf("faild to encrypt private key key with %w", err)
				}
				pkey = base64.StdEncoding.EncodeToString(pkdata)
			}
		}
	}

	bundle.Privatekey = pkey
	bundle.Swarmkey = base64.StdEncoding.EncodeToString(skdata)
	bundle.Domains.Key.Private = base64.StdEncoding.EncodeToString(dvsdata)
	bundle.Domains.Key.Public = base64.StdEncoding.EncodeToString(dvpdata)

	var out io.Writer = os.Stdout
	if ctx.Args().Present() {
		filename := ctx.Args().First()
		f, err := os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0460)
		if err != nil {
			return fmt.Errorf("fail to open %s with %w", filename, err)
		}
		defer f.Close()
		out = f
	}

	err = yaml.NewEncoder(out).Encode(bundle)
	if err != nil {
		return fmt.Errorf("fail to marshal configuration with %w", err)
	}

	return nil
}
