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

	memory, err := getMemoryAndType(ctx, prevString, new)
	if err != nil {
		return 0, err
	}
	return schemaCommon.StringToUnits(memory)
}

func getMemoryAndType(c *cli.Context, oldSize string, isNew bool) (size string, err error) {
	// Uppercase the relative flags
	flags.ToUpper(c, flags.Memory, flags.MemoryUnit)

	var memory, unitType string
	if isNew {
		memory = RequiredString(c, MemoryPrompt, getOrAskForMemory)
		if _, parseErr := schemaCommon.StringToUnits(memory); parseErr != nil {
			unitType, err = GetOrAskForSelection(c, flags.MemoryUnit.Name, UnitTypePrompt, common.SizeUnitTypes)
			if err != nil {
				return "", err
			}
		} else {
			return memory, nil
		}
		return memory + unitType, nil
	}
	memory = RequiredString(c, MemoryPrompt, getOrAskForMemory, oldSize)
	if _, parseErr := schemaCommon.StringToUnits(memory); parseErr != nil {
		var prevType string
		for _, o := range common.SizeUnitTypes {
			if strings.Contains(strings.ToUpper(oldSize), o) {
				prevType = o
			}
		}
		unitType, err = GetOrAskForSelection(c, flags.MemoryUnit.Name, UnitTypePrompt, common.SizeUnitTypes, strings.ToUpper(prevType))
		if err != nil {
			return "", err
		}
	} else {
		return memory, nil
	}
	return memory + unitType, nil
}
