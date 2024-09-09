package storage

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	storageFlags "github.com/taubyte/tau/tools/tau/flags/storage"
	storageI18n "github.com/taubyte/tau/tools/tau/i18n/storage"
	storageLib "github.com/taubyte/tau/tools/tau/lib/storage"
	storagePrompts "github.com/taubyte/tau/tools/tau/prompts/storage"
	storageTable "github.com/taubyte/tau/tools/tau/table/storage"
)

func (link) New() common.Command {
	return (&resources.New[*structureSpec.Storage]{
		PromptsNew:        storagePrompts.New,
		TableConfirm:      storageTable.Confirm,
		PromptsCreateThis: storagePrompts.CreateThis,
		LibNew:            storageLib.New,
		I18nCreated:       storageI18n.Created,

		UniqueFlags: flags.Combine(
			flags.MatchRegex,
			flags.Match,
			storageFlags.Public,
			flags.Size,
			flags.SizeUnit,
			storageFlags.BucketType,
			storageFlags.Versioning, // BucketType Object
			storageFlags.TTL,        // BucketType Streaming
		),
	}).Default()
}
