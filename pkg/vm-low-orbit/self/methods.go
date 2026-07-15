package self

import (
	"context"

	"github.com/taubyte/tau/core/vm"
	spec "github.com/taubyte/tau/pkg/specs/common"
)

func (f *Factory) selfApplicationSize(ctx context.Context, module vm.Module, applicationSizePtr uint32) uint32 {
	return uint32(f.WriteStringSize(module, applicationSizePtr, f.parent.Context().Application()))
}

func (f *Factory) selfApplication(ctx context.Context, module vm.Module, applicationPtr uint32) uint32 {
	return uint32(f.WriteString(module, applicationPtr, f.parent.Context().Application()))
}

func (f *Factory) selfProjectSize(ctx context.Context, module vm.Module, projectSizePtr uint32) uint32 {
	return uint32(f.WriteStringSize(module, projectSizePtr, f.parent.Context().Project()))
}

func (f *Factory) selfProject(ctx context.Context, module vm.Module, projectPtr uint32) uint32 {
	return uint32(f.WriteString(module, projectPtr, f.parent.Context().Project()))
}

func (f *Factory) selfIdSize(ctx context.Context, module vm.Module, functionSizePtr uint32) uint32 {
	return uint32(f.WriteStringSize(module, functionSizePtr, f.parent.Context().Resource()))
}

func (f *Factory) selfId(ctx context.Context, module vm.Module, functionPtr uint32) uint32 {
	return uint32(f.WriteString(module, functionPtr, f.parent.Context().Resource()))
}

// TODO: update SDK
func (f *Factory) selfBranchSize(ctx context.Context, module vm.Module, branchSizePtr uint32) uint32 {
	return uint32(f.WriteStringSliceSize(module, branchSizePtr, spec.DefaultBranches))
}

// TODO: update SDK
func (f *Factory) selfBranch(ctx context.Context, module vm.Module, branchPtr uint32) uint32 {
	return uint32(f.WriteStringSlice(module, branchPtr, spec.DefaultBranches))
}

func (f *Factory) selfCommitSize(ctx context.Context, module vm.Module, branchSizePtr uint32) uint32 {
	return uint32(f.WriteStringSize(module, branchSizePtr, f.parent.Context().Commit()))
}

func (f *Factory) selfCommit(ctx context.Context, module vm.Module, branchPtr uint32) uint32 {
	return uint32(f.WriteString(module, branchPtr, f.parent.Context().Commit()))
}
