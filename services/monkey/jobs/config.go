package jobs

import (
	"fmt"
	"io"

	_ "github.com/taubyte/tau/pkg/builder"
	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	tccCompiler "github.com/taubyte/tau/pkg/tcc/taubyte/v1"
	tcc "github.com/taubyte/tau/utils/tcc"
)

func (c config) handle() error {
	// WithCloud drives pass1.Cloud: promotes the matching `clouds.<fqdn>`
	// entry to flat `account` / `plan` scalars; drops the rest. Empty
	// FQDN = no-op (dream / local).
	opts := []tccCompiler.Option{
		tccCompiler.WithLocal(c.gitDir),
		tccCompiler.WithBranch(c.Job.Meta.Repository.Branch),
		tccCompiler.WithCloud(c.NetworkFqdn),
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

	// Validate the cloud/account/plan binding. Both code- and config-repo
	// pushes run the same check so neither path can bypass it.
	project, err := projectSchema.Open(projectSchema.SystemFS(c.gitDir))
	if err != nil {
		return fmt.Errorf("opening project from path `%s` failed with: %w", c.gitDir, err)
	}
	if err := c.checkAccountPlan(project); err != nil {
		return err
	}

	flat := obj.Flat()
	object, ok := flat["object"].(map[string]any)
	if !ok {
		return fmt.Errorf("object not found in flat result")
	}
	indexes, ok := flat["indexes"].(map[string]any)
	if !ok {
		return fmt.Errorf("indexes not found in flat result")
	}

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
