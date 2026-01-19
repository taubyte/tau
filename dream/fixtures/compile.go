package fixtures

import (
	"context"
	"fmt"
	"regexp"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/dream"
	commonTest "github.com/taubyte/tau/dream/helpers"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	tcc "github.com/taubyte/tau/utils/tcc"
)

var generatedDomainRegExp = regexp.MustCompile(`^[^.]+\.g\.tau\.link$`)

// injectWithFS compiles and publishes a project using TCC from a filesystem
func injectWithFS(fs afero.Fs, configPath string, branch string, commit string, simple *dream.Simple) error {
	// Create compiler with options
	compiler, err := tccCompiler.New(
		tccCompiler.WithVirtual(fs, configPath),
		tccCompiler.WithBranch(branch),
	)
	if err != nil {
		return fmt.Errorf("new config compiler failed with: %w", err)
	}

	// Compile
	ctx := context.Background()
	obj, validations, err := compiler.Compile(ctx)
	if err != nil {
		return fmt.Errorf("config compiler compile failed with: %w", err)
	}

	// Extract project ID from validations
	projectID, err := tcc.ExtractProjectID(validations)
	if err != nil {
		return fmt.Errorf("extracting project ID failed with: %w", err)
	}

	// Process DNS validations (dev mode)
	err = tcc.ProcessDNSValidations(
		validations,
		generatedDomainRegExp,
		true, // dev mode
		nil,  // no DV key needed in dev mode
	)
	if err != nil {
		return fmt.Errorf("processing DNS validations failed with: %w", err)
	}

	// Extract object and indexes from Flat()
	flat := obj.Flat()
	object, ok := flat["object"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("object not found in flat result")
	}

	indexes, ok := flat["indexes"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("indexes not found in flat result")
	}

	// Get TNS client
	tns, err := simple.TNS()
	if err != nil {
		return fmt.Errorf("getting TNS client failed with: %w", err)
	}

	// Publish to TNS
	err = tcc.Publish(
		tns,
		object,
		indexes,
		projectID,
		branch,
		commit,
	)
	if err != nil {
		return fmt.Errorf("publishing compiled config failed with: %w", err)
	}

	return nil
}

// inject compiles and publishes a project using TCC
// fs is the filesystem where the project was created (from tcc.GenerateProject)
// configPath is the root path within the filesystem where config.yaml lives
func inject(fs afero.Fs, configPath string, simple *dream.Simple) error {
	fakeMeta := commonTest.ConfigRepo.HookInfo
	fakeMeta.Repository.Provider = "github"
	fakeMeta.Repository.Branch = "main"
	fakeMeta.HeadCommit.ID = "testCommit"

	return injectWithFS(fs, configPath, fakeMeta.Repository.Branch, fakeMeta.HeadCommit.ID, simple)
}
