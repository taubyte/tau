package raft

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// TimeoutConfig allows fine-grained control over Raft timing
type TimeoutConfig struct {
	// HeartbeatTimeout specifies the time in follower state without
	// a leader before we attempt an election
	HeartbeatTimeout time.Duration `yaml:"heartbeat_timeout"`

	// ElectionTimeout specifies the time in candidate state without
	// leader contact before we attempt another election
	ElectionTimeout time.Duration `yaml:"election_timeout"`

	// CommitTimeout controls the time without an Apply operation
	// before heartbeat is sent to ensure timely commit
	CommitTimeout time.Duration `yaml:"commit_timeout"`

	// LeaderLeaseTimeout is used to control how long the "lease"
	// lasts for leader without being able to contact quorum
	LeaderLeaseTimeout time.Duration `yaml:"leader_lease_timeout"`

	// SnapshotInterval controls how often we check if we should
	// perform a snapshot
	SnapshotInterval time.Duration `yaml:"snapshot_interval"`

	// SnapshotThreshold controls how many outstanding logs there
	// must be before we perform a snapshot
	SnapshotThreshold uint64 `yaml:"snapshot_threshold"`

	// TrailingLogs is how many entries a snapshot leaves in the log behind it, so
	// a follower slightly behind catches up from the log rather than a snapshot.
	// Everything older is deleted from the log store.
	TrailingLogs uint64 `yaml:"trailing_logs"`
}

// DefaultTimeoutConfig is the default timeout configuration, tuned for
// worldwide distributed clusters.
var DefaultTimeoutConfig = TimeoutConfig{
	HeartbeatTimeout:   15 * time.Second,
	ElectionTimeout:    30 * time.Second,
	CommitTimeout:      15 * time.Second,
	LeaderLeaseTimeout: 15 * time.Second,
	SnapshotInterval:   10 * time.Minute,
	SnapshotThreshold:  32768,
	TrailingLogs:       10240,
}

// Timing is how a node's groups time out, as its operator writes it; zero is raft's
// defaults.
type Timing struct {
	Timeouts           TimeoutConfig      `yaml:"timeouts"`
	BootstrapTimeout   time.Duration      `yaml:"bootstrap_timeout"`
	QuorumLossRecovery QuorumLossRecovery `yaml:"quorum_loss_recovery,omitempty"`
}

// QuorumLossRecovery is what a group does once fewer than a quorum of its members
// answer: Manual, the zero value, waits for them to come back; a duration has the
// members that remain form a new group carrying what they hold once they have had
// no leader for that long. A partition that long looks the same as members gone,
// so the two sides then hold two histories, merged when they meet again.
type QuorumLossRecovery time.Duration

// Manual is a group that waits for its members however long they are gone.
const Manual QuorumLossRecovery = 0

// UnmarshalYAML reads "manual" or a duration.
func (r *QuorumLossRecovery) UnmarshalYAML(value *yaml.Node) error {
	if value.Value == "manual" {
		*r = Manual
		return nil
	}
	d, err := time.ParseDuration(value.Value)
	if err != nil || d < 0 {
		return fmt.Errorf("quorum_loss_recovery is manual or a duration, not %q", value.Value)
	}
	*r = QuorumLossRecovery(d)
	return nil
}

// MarshalYAML writes what UnmarshalYAML reads.
func (r QuorumLossRecovery) MarshalYAML() (any, error) {
	if r == Manual {
		return "manual", nil
	}
	return time.Duration(r).String(), nil
}

// Options are what a group is opened with on this timing.
func (t Timing) Options() []Option {
	var options []Option
	if t.Timeouts != (TimeoutConfig{}) {
		options = append(options, WithTimeouts(t.Timeouts))
	}
	if t.BootstrapTimeout != 0 {
		options = append(options, WithBootstrapTimeout(t.BootstrapTimeout))
	}
	if t.QuorumLossRecovery != Manual {
		options = append(options, WithQuorumLossRecovery(time.Duration(t.QuorumLossRecovery)))
	}
	return options
}
