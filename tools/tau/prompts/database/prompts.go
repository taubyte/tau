package databasePrompts

import (
	databaseFlags "github.com/taubyte/tau/tools/tau/flags/database"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func GetOrRequireAMatch(ctx *cli.Context, prev ...string) string {
	return prompts.GetOrRequireAMatch(ctx, DatabaseMatch, prev...)
}

func GetEncryption(ctx *cli.Context, prev ...bool) bool {
	return prompts.GetOrAskForBool(ctx, databaseFlags.Encryption.Name, EncryptionPrompt, prev...)
}

func GetOrRequireAnEncryptionKey(c *cli.Context, prev ...string) string {
	return prompts.GetOrRequireAString(c, databaseFlags.EncryptionKey.Name, EncryptionKeyPrompt, nil, prev...)
}
