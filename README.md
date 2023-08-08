# tau

[![Release](https://img.shields.io/github/release/taubyte/tau.svg)](https://github.com/taubyte/tau/releases)
[![License](https://img.shields.io/github/license/taubyte/tau)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/taubyte/tau)](https://goreportcard.com/report/taubyte/tau)
[![GoDoc](https://godoc.org/github.com/taubyte/tau?status.svg)](https://pkg.go.dev/github.com/taubyte/tau)
[![Discord](https://img.shields.io/discord/973677117722202152?color=%235865f2&label=discord)](https://discord.gg/taubyte)

`tau` represents the Taubyte Node's implementation. When interconnected, these nodes form a network that's cloud computing-ready, offering features such as:
 - Serverless WebAssembly Functions
 - Website/Frontend Hosting
 - Object Storage
 - K/V Database
 - Pub-Sub Messaging

For detailed documentation, visit [https://tau.how](https://tau.how).

## Getting started
### Installation

#### From source
```bash
$ go install github.com/taubyte/tau
```

#### From binary
 - Download the [latest release](https://github.com/taubyte/tau/releases)
 - Place the binary in one of the `$PATH` directories
 - Ensure it's executable (`chmod +x`)

### Filesystem Structure
We at Taubyte prioritize convention over configuration. Hence, we've pre-defined the filesystem structure and its location as follows:
```
/tb
├── bin
│   └── tau
├── cache
├── config
│   ├── <shape-1>.yaml
│   ├── <shape-2>.yaml
│   └── keys
│       ├── private.key
│       ├── public.pem
│       └── swarm.key
├── logs
├── plugins
└── storage
```

> Note: If you prefer a different location, utilize the `--root` option.

### Configuration
Configuration files for `tau` are located at `/tb/config/shape-name.yaml`. Here's an example:

```yaml
privatekey: CAESQJxQzCe/N/C8A5TIgrL9F0p5iG...KzYW9pygBCTJSuezIc6w/TT/unZKJ5mo=
swarmkey: keys/test_swarm.key
protocols: [patrick,substrate,tns,monkey,seer,auth]
p2p-listen: [/ip4/0.0.0.0/tcp/8100]
p2p-announce: [/ip4/127.0.0.1/tcp/8100]
ports:
  main: 8100
  lite: 8102
  ipfs: 8104
location:
  lat: 120
  long: 21
http-listen: 0.0.0.0:443
network-url: example.com
domains:
  key:
    private: keys/test.key
  services: ^[^.]+\.tau\.example\.com
  generated: g.example.com
  whitelist:
    postfix: [test.com]
    regex:
      - '^[^.]+\.test\.example\.com'
```

### Running `tau`
Execute a `tau` node with:
```bash
tau start --shape shape-name
```
For an alternative root to `/tb`:
```bash
$ tau start --shape shape-name --root path-to-root
```

### Systemd Configuration
To ensure that `tau` runs as a service and starts automatically upon system boot, you can set it up as a `systemd` service. 

1. Create a new service file:
```bash
$ sudo nano /etc/systemd/system/tau.service
```

2. Add the following content to the file:
```plaintext
[Unit]
Description=Taubyte Node Service
After=network.target

[Service]
ExecStart=/path/to/tau/bin tau start --shape shape-name --root path-to-root
User=username
Restart=on-failure

[Install]
WantedBy=multi-user.target
```
Replace `/path/to/tau/bin` with the actual path to your `tau` binary and `username` with the name of the user running `tau`.

3. Enable and start the service:
```bash
$ sudo systemctl enable tau
$ sudo systemctl start tau
```

To check the status:
```bash
$ sudo systemctl status tau
```

This ensures `tau` runs consistently, even after system reboots.