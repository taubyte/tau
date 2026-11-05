// Package policy decides which v2 RPCs the bridge forwards.
//
// The bridge calls every service AS ITS OWN SWARM PEER, and in a community
// cloud a swarm peer is trusted. So whoever reaches the local listener borrows
// that trust, and this table is the only thing narrowing it. It is an explicit
// list rather than a naming rule ("Get* is a read") because the names lie in
// both directions: RepositoryService.Get returns a deploy key, and
// SecretsService.Key is a read nobody outside the swarm needs.
//
// A method missing from the table is refused. policy_test.go holds the table
// equal to the generated descriptors, so a new RPC fails the build of this
// module's tests until someone classifies it.
package policy

import (
	"fmt"
	"strings"
)

type Class int

const (
	// Unknown is a method the table does not name. It is never forwarded.
	Unknown Class = iota
	// Read observes cloud metadata: projects, deployments, jobs, fleet state.
	Read
	// Sensitive is a read that returns key material or tenant data:
	// secrets, certificates, deploy keys, hook secrets, database contents.
	Sensitive
	// Write mutates state, executes code, or is an internal replication or
	// consensus call no outside client should drive.
	Write
)

func (c Class) String() string {
	switch c {
	case Read:
		return "read"
	case Sensitive:
		return "sensitive"
	case Write:
		return "write"
	}
	return "unknown"
}

const (
	R = Read
	S = Sensitive
	W = Write
)

// Table maps a full service name to its methods' classes.
var Table = map[string]map[string]Class{
	"tau.register.v2.RegisterService": {
		"Link": W, "Project": R, "Repository": R, "ProjectsOf": R, "ListProjects": R,
		"Publish": W, "Resolve": R, "Object": R, "Environments": R,
		"ResolveHost": R, "ResolveView": R, "Assigned": R, "Manifest": R, "ResolveFleet": R,
		"Grant": W, "Revoke": W, "Grants": R, "Role": R,
	},
	"tau.plm.v2.QueueService": {"Enqueue": W},
	"tau.plm.v2.JobService": {
		"Get": R, "List": R, "Cancel": W, "Retry": W, "Artifact": R, "Follow": R,
	},
	// A builder's runs: what plm hands it, which no outside client drives.
	"tau.builder.v2.BuilderService": {"Plan": W, "Build": W, "Cancel": W, "Seal": W, "Follow": R},
	// Verifying says who a token is.
	"tau.auth.v2.IdentityService": {"Admit": S, "Verify": S},
	// Every call that ends in a session or a forge token mints a credential,
	// so it is a write.
	"tau.auth.v2.SessionService": {
		"Providers": R, "Login": W, "Refresh": W, "Logout": W, "Authorize": W,
		"Exchange": W, "DeviceStart": W, "DevicePoll": W, "Pasted": W,
		"Whoami": S, "Renew": W,
	},
	"tau.auth.v2.UserService": {
		"ListUsers": S, "CreateUser": W, "DeleteUser": W, "SetRole": W,
		"SetDisabled": W, "SetPassword": W, "CreateInvite": W, "ListInvites": S,
		"DeleteInvite": W, "CreateToken": W, "ListTokens": S, "RevokeToken": W,
	},
	"tau.raft.v2.RaftService": {
		"Set": W, "Get": R, "Delete": W, "Keys": R, "ExchangePeers": W,
		"JoinVoter": W, "ClusterInfo": R, "ExportFSM": S, "HealAck": W,
	},
	"tau.database.v2.DatabaseService": {
		"KVGet": S, "KVPut": W, "KVDelete": W, "KVList": S, "KVDigest": R, "Hold": W,
	},
	"tau.primitives.v2.PrimitivesService": {
		"QueueSend": W, "QueueClaim": W, "QueueSettle": W, "QueueDepth": R,
		"LogAppend": W, "LogRead": S, "CronTask": W,
	},
	"tau.secrets.v2.SecretsService": {
		"Key": S, "Put": W, "Get": S, "Previous": S, "Partial": S, "Delete": W, "List": S,
	},
	"tau.secrets.v2.DealerService": {"Contribute": W, "Replay": S},
	"tau.sqlsync.v2.SQLSyncService": {
		"ExchangeVV": W, "GetOps": S, "ExchangeAcks": W, "GetSnapshot": S,
	},
	"tau.worker.v2.WorkerService": {
		"Invoke": W, "Transform": W, "Route": W, "Direct": W, "Shape": W,
	},
}

// Parse splits a ConnectRPC/gRPC path "/pkg.Service/Method".
func Parse(path string) (service, method string, err error) {
	p := strings.TrimPrefix(path, "/")
	i := strings.LastIndex(p, "/")
	if i <= 0 || i == len(p)-1 || strings.Contains(p[:i], "/") {
		return "", "", fmt.Errorf("%q is not /<service>/<method>", path)
	}
	return p[:i], p[i+1:], nil
}

// Of is the class of a method.
func Of(service, method string) Class {
	return Table[service][method]
}

// Allow is what an operator opened beyond reads.
type Allow struct {
	Writes    bool
	Sensitive bool
}

// Permits says whether a method of class c may be forwarded.
func (a Allow) Permits(c Class) bool {
	switch c {
	case Read:
		return true
	case Sensitive:
		return a.Sensitive
	case Write:
		return a.Writes
	}
	return false
}
