# tau

tau is a self-hosted cloud platform: a single node binary that, run on one
machine or many, builds projects from git, serves their functions, websites,
containers and data, and routes traffic to them.

## The node

`cmd/taud` is the node binary. The developer CLI, `tau`, is its own module under
`tools/tau` (`go install github.com/taubyte/tau/tools/tau@latest`).

```sh
go build ./cmd/taud
./taud config > node.yaml     # print an example node config
./taud start -c node.yaml     # run a node from it (--root overrides the data directory)
```

Everything a node runs is decided by its config file (`instance/boot`). Its
`services` list picks from the services this tree ships:

| service          | package              | what it does                                              |
|------------------|----------------------|-----------------------------------------------------------|
| `register`       | `services/register`  | holds what a project published                            |
| `worker`         | `services/worker`    | runs a project's code                                     |
| `discovery`      | `services/discovery` | gossips what every node runs and how loaded it is         |
| `gateway`        | `services/gateway`   | HTTP ingress, routed from discovery                       |
| `proxy`          | `services/proxy`     | raw TCP ingress to a workload's socket                    |
| `dns`            | `services/dns`       | answers queries from discovery                            |
| `auth`           | `services/auth`      | projects, repositories, hooks, domains, certificates      |
| `secrets`        | `services/secrets`   | a project's secrets                                       |
| `database`       | `services/database`  | a project's databases                                     |
| `primitives`     | `services/primitives`| a project's queues and logs                               |
| `plm`            | `services/plm`       | pipelines: webhooks, the job API, the queue and job records |
| `builder`        | `services/builder`   | clones and runs builds, keeping each run's log            |

`node/` composes the
services into a node; `instance/placement` is its request router. Services talk to
each other over ConnectRPC carried on libp2p streams; their schemas are the
`.proto` files under `pkg/proto` (regenerate with `make proto-v2`).

`tools/spore-drive` deploys nodes to a fleet of hosts over SSH, with
JavaScript and Python clients under `tools/spore-drive/clients`.

## Projects

A project is declared in yaml and compiled by `pkg/tcc`. The projects under
`instance/dream/projects/` are run end to end by the tests in `instance/dream`: compile,
publish, resolve and serve, on an in-process multi-node cloud.

## Tests

| target                         | what it runs                                                  |
|--------------------------------|---------------------------------------------------------------|
| `make test`                    | every package, untagged                                       |
| `make test-dreaming`           | the multi-node suites (`-tags dreaming`)                      |
| `make test-raft`               | `pkg/raft`'s integration suite                                |
| `make test-docker`             | the container conformance suite on docker                     |
| `make test-containerd`         | the same suite on containerd                                  |
| `make test-application`        | an application served end to end by a container on docker    |
| `make test-build`              | the build service's builds in real containers                 |
| `make test-external-databases` | projects against real postgres and mysql servers in docker    |
| `make proto-v2-check`          | the generated RPC stubs match `pkg/proto`                     |

`test-containerd`, `test-application` and `test-build` need root: they run the
test binary through `scripts/as-root`, which uses root or passwordless sudo
when it has them and otherwise prints `SKIP` and exits 0. The binary is always
built as the user. `scripts/rtest` runs any of these on another machine, and
`scripts/cap` bounds a run's memory.

## Community

Questions and discussion: [Discord](https://discord.gg/KbN3KN7kpQ).
See [CONTRIBUTING.md](CONTRIBUTING.md) to contribute.

## License

Apache 2.0, see [LICENSE](LICENSE) and [COPYRIGHT](COPYRIGHT).
