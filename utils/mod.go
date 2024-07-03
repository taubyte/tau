package utils

import (
	"fmt"
	"os"

	"golang.org/x/mod/modfile"
)

type ModFileOps func(*modfile.File) error

func ModRename(newModuleName string) ModFileOps {
	return func(modFile *modfile.File) error {
		if err := modFile.AddModuleStmt(newModuleName); err != nil {
			return fmt.Errorf("could not add module statement: %v", err)
		}
		return nil
	}
}

func Replace(oldPath, newPath string) ModFileOps {
	return func(modFile *modfile.File) error {
		if err := modFile.AddReplace(oldPath, "", newPath, ""); err != nil {
			return fmt.Errorf("could not add module statement: %v", err)
		}
		return nil
	}
}

func DuplicateModFile(srcFile, dstFile string, ops ...ModFileOps) error {
	// Read the go.mod file content
	data, err := os.ReadFile(srcFile)
	if err != nil {
		return fmt.Errorf("could not read source file: %v", err)
	}

	// Parse the go.mod file content
	modFile, err := modfile.Parse(srcFile, data, nil)
	if err != nil {
		return fmt.Errorf("could not parse go.mod file: %v", err)
	}

	for _, op := range ops {
		if err = op(modFile); err != nil {
			return err
		}
	}

	// Format the modified go.mod file content
	modifiedData, err := modFile.Format()
	if err != nil {
		return fmt.Errorf("could not format go.mod file: %v", err)
	}

	// Write the modified content to the destination file
	err = os.WriteFile(dstFile, modifiedData, 0644)
	if err != nil {
		return fmt.Errorf("could not write to destination file: %v", err)
	}

	return nil
}
