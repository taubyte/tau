package prompts

import (
	"strings"

	"github.com/taubyte/tau/pkg/cli/common"
	schemaCommon "github.com/taubyte/tau/pkg/schema/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/urfave/cli/v2"
)

// TODO make generic with size

func getOrAskForMemory(c *cli.Context, prompt string, prev ...string) string {
	var ret string
	for {
		ret = GetOrAskForAStringValue(c, flags.Memory.Name, prompt, prev...)
		if variableMaxSizeValidator(ret) {
			break
		}

		// Unset the flag to prevent it from circling back into the prompt
		if c.IsSet(flags.Memory.Name) {
			err := c.Set(flags.Memory.Name, "")
			if err != nil {
				panic(err)
			}
		}
	}
	return ret
}

func GetOrRequireMemoryAndType(ctx *cli.Context, new bool, prev ...uint64) (uint64, error) {
	var prevString string
	if len(prev) > 0 {
		prevString = schemaCommon.UnitsToString(prev[0])
	}

	memory := getMemoryAndType(ctx, prevString, new)
	return schemaCommon.StringToUnits(memory)
}

func getMemoryAndType(c *cli.Context, oldSize string, isNew bool) (size string) {
	// Uppercase the relative flags
	flags.ToUpper(c, flags.Memory, flags.MemoryUnit)

	var memory, unitType string
	if isNew {
		memory = RequiredString(c, MemoryPrompt, getOrAskForMemory)
		if _, err := schemaCommon.StringToUnits(memory); err != nil {
			unitType = GetOrAskForSelection(c, flags.MemoryUnit.Name, UnitTypePrompt, common.SizeUnitTypes)
		} else {
			return memory
		}
		return memory + unitType
	} else {
		memory = RequiredString(c, MemoryPrompt, getOrAskForMemory, oldSize)
		if _, err := schemaCommon.StringToUnits(memory); err != nil {
			var prevType string
			for _, o := range common.SizeUnitTypes {
				if strings.Contains(strings.ToUpper(oldSize), o) {
					prevType = o
				}
			}
			unitType = GetOrAskForSelection(c, flags.MemoryUnit.Name, UnitTypePrompt, common.SizeUnitTypes, strings.ToUpper(prevType))
		} else {
			return memory
		}
		return memory + unitType
	}
}
