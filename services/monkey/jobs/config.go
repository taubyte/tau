package jobs

import (
	"fmt"
	"io"

	_ "github.com/taubyte/tau/pkg/builder"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	tcc "github.com/taubyte/tau/utils/tcc"
)

func (c config) handle() error {
	// Create compiler with options
	opts := []tccCompiler.Option{
		tccCompiler.WithLocal(c.gitDir),
		tccCompiler.WithBranch(c.Job.Meta.Repository.Branch),
	}

	compiler, err := tccCompiler.New(opts...)
	if err != nil {
		return fmt.Errorf("new config compiler failed with: %s", err.Error())
	}

	// Compile
	obj, validations, err := compiler.Compile(c.ctx)
	if err != nil {
		// Write error logs
		logs := tcc.Logs(err)
		logs.Seek(0, io.SeekStart)
		io.Copy(c.LogFile, logs)
		return fmt.Errorf("config compiler compile failed with: %s", err.Error())
	}

	// Process project ID validation
	_, err = tcc.ProcessProjectIDValidation(validations, c.ProjectID)
	if err != nil {
		return fmt.Errorf("processing project ID validation failed with: %s", err.Error())
	}

	// Process DNS validations
	err = tcc.ProcessDNSValidations(
		validations,
		c.GeneratedDomainRegExp,
		c.Monkey.Dev(),
		c.DVPublicKey,
	)
	if err != nil {
		return fmt.Errorf("processing DNS validations failed with: %s", err.Error())
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

	// Publish to TNS
	err = tcc.Publish(
		c.Tns,
		object,
		indexes,
		c.ProjectID,
		c.Job.Meta.Repository.Branch,
		c.Job.Meta.HeadCommit.ID,
	)
	if err != nil {
		return fmt.Errorf("publishing compiled config failed with: %s", err.Error())
	}

	return nil
}
