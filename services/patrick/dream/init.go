package dream

import (
	"context"
	"path"
	"time"

	iface "github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/common"
	tauConfig "github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/raft"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	servicesCommon "github.com/taubyte/tau/services/common"
	patrick "github.com/taubyte/tau/services/patrick"
)

func init() {
	if err := dream.Registry.Set(commonSpecs.Patrick, createPatrickService, nil); err != nil {
		panic(err)
	}
}

// patrickWithRaft wraps patrick service and closes the raft cluster on Close (dream-owned cluster).
type patrickWithRaft struct {
	*patrick.PatrickService
	raftCluster raft.Cluster
}

func (p *patrickWithRaft) Close() error {
	err := p.PatrickService.Close()
	if p.raftCluster != nil {
		if closeErr := p.raftCluster.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}
	return err
}

func createPatrickService(u *dream.Universe, config *iface.ServiceConfig) (iface.Service, error) {
	// Used to test cancel job on go-patrick-http
	if result, ok := config.Others["delay"]; ok {
		if result == 1 {
			servicesCommon.DelayJob = true
		}
	}

	// Used to test retry job on go-patrick-http
	if result, ok := config.Others["retry"]; ok {
		if result == 1 {
			servicesCommon.RetryJob = true
		}
	}

	cfg, err := common.NewConfig(u, config)
	if err != nil {
		return nil, err
	}

	// Dream runs with raft active; default cluster is "main". Create node and raft so Patrick has a job queue.
	node, err := tauConfig.NewNode(u.Context(), cfg, path.Join(cfg.Root(), servicesCommon.Patrick))
	if err != nil {
		return nil, err
	}
	raftCluster, err := common.NewRaftCluster(node, cfg.Cluster())
	if err != nil {
		return nil, err
	}
	cfg.SetNode(node)
	cfg.SetRaftCluster(raftCluster)

	// Wait for single-node bootstrap to take effect (raft applies config asynchronously).
	waitCtx, waitCancel := context.WithTimeout(u.Context(), 10*time.Second)
	err = raftCluster.WaitForLeader(waitCtx)
	waitCancel()
	if err != nil {
		raftCluster.Close()
		return nil, err
	}

	srv, err := patrick.New(u.Context(), cfg)
	if err != nil {
		raftCluster.Close()
		return nil, err
	}
	return &patrickWithRaft{PatrickService: srv, raftCluster: raftCluster}, nil
}
