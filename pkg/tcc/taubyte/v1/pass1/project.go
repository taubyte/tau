package pass1

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

type project struct{}

func Project() transform.Transformer[object.Refrence] {
	return &project{}
}

func (p *project) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	o.Delete("tags") // TODO: delete. compat with old config-compiler

	// Emit project ID validation
	projectId, err := o.GetString("id")
	if err != nil {
		return nil, fmt.Errorf("project id is not a string: %w", err)
	}

	// Get validations store
	validationsStore := ct.Store().Validators()
	validations := validationsStore.Get()

	// Emit validation request for project ID
	validations = append(validations, engine.NewNextValidation(
		"project_id",
		projectId,
		"project_id",
		map[string]interface{}{},
	))

	// Store validations back
	_, err = validationsStore.Set(validations)
	if err != nil {
		return nil, fmt.Errorf("storing validations failed with %w", err)
	}

	return o, nil
}
