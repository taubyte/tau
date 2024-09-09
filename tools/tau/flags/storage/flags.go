package storageFlags

import (
	"github.com/taubyte/tau/tools/tau/flags"
	storageLib "github.com/taubyte/tau/tools/tau/lib/storage"
	"github.com/urfave/cli/v2"
)

var Versioning = &flags.BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name:     "versioning",
		Aliases:  []string{"v"},
		Category: storageLib.BucketObject,
	},
}

var Public = &flags.BoolWithInverseFlag{
	BoolFlag: &cli.BoolFlag{
		Name:    "public",
		Aliases: []string{"p"},
	},
}

var BucketType = &cli.StringFlag{
	Name:        "bucket",
	Aliases:     []string{"b"},
	Usage:       flags.UsageOneOfOption(storageLib.Buckets),
	DefaultText: storageLib.DefaultBucket,
}

var TTL = &cli.StringFlag{
	Name:    flags.Timeout.Name,
	Aliases: flags.Timeout.Aliases,
	Usage:   flags.Timeout.Usage,

	Category: storageLib.BucketStreaming,
}
