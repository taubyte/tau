package self

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

func (f *Factory) W_selfApplicationSize(ctx context.Context, module vm.Module, applicationSizePtr uint32) errno.Error {
	return f.WriteStringSize(module, applicationSizePtr, f.parent.Context().Application())
}

func (f *Factory) W_selfApplication(ctx context.Context, module vm.Module, applicationPtr uint32) errno.Error {
	return f.WriteString(module, applicationPtr, f.parent.Context().Application())
}

func (f *Factory) W_selfProjectSize(ctx context.Context, module vm.Module, projectSizePtr uint32) errno.Error {
	return f.WriteStringSize(module, projectSizePtr, f.parent.Context().Project())
}

func (f *Factory) W_selfProject(ctx context.Context, module vm.Module, projectPtr uint32) errno.Error {
	return f.WriteString(module, projectPtr, f.parent.Context().Project())
}

func (f *Factory) W_selfIdSize(ctx context.Context, module vm.Module, functionSizePtr uint32) errno.Error {
	return f.WriteStringSize(module, functionSizePtr, f.parent.Context().Resource())
}

func (f *Factory) W_selfId(ctx context.Context, module vm.Module, functionPtr uint32) errno.Error {
	return f.WriteString(module, functionPtr, f.parent.Context().Resource())
}

// TODO: update SDK
func (f *Factory) W_selfBranchesSize(ctx context.Context, module vm.Module, branchSizePtr uint32) errno.Error {
	return f.WriteStringSliceSize(module, branchSizePtr, spec.DefaultBranches)
}

// TODO: update SDK
func (f *Factory) W_selfBranches(ctx context.Context, module vm.Module, branchPtr uint32) errno.Error {
	return f.WriteStringSlice(module, branchPtr, spec.DefaultBranches)
}

func (f *Factory) W_selfCommitSize(ctx context.Context, module vm.Module, branchSizePtr uint32) errno.Error {
	return f.WriteStringSize(module, branchSizePtr, f.parent.Context().Commit())
}

func (f *Factory) W_selfCommit(ctx context.Context, module vm.Module, branchPtr uint32) errno.Error {
	return f.WriteString(module, branchPtr, f.parent.Context().Commit())
}
