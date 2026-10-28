package drive

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	host "github.com/taubyte/tau/pkg/mycelium/host"
	"github.com/taubyte/tau/tools/spore-drive/course"
	preader "github.com/taubyte/tau/tools/spore-drive/drive/progress"
	"google.golang.org/protobuf/proto"
	"gopkg.in/ini.v1"
	"gopkg.in/yaml.v3"
)

var systemdResolvedRegexp = regexp.MustCompile(`.*:53[\s\t].*systemd-.*`)

// dnsResolvers is the ordered list of candidate public DNS servers. Each one
// is probed from the target host before deployment; only the resolvers that
// actually answer get written into /etc/systemd/resolved.conf, so a host
// behind a firewall that blocks (say) 1.1.1.1 still gets a working setup.
var dnsResolvers = []string{
	"1.1.1.1",         // Cloudflare
	"1.0.0.1",         // Cloudflare secondary
	"8.8.8.8",         // Google
	"8.8.4.4",         // Google secondary
	"9.9.9.9",         // Quad9
	"149.112.112.112", // Quad9 secondary
	"208.67.222.222",  // OpenDNS
	"208.67.220.220",  // OpenDNS secondary
}

// probeDNSResolvers returns the subset of dnsResolvers that successfully
// resolve google.com from the target host within a 5s timeout each.
func probeDNSResolvers(ctx context.Context, r remoteHost) []string {
	var working []string
	for _, dns := range dnsResolvers {
		out, err := r.Execute(ctx, "dig", "+short", "+timeout=5", "@"+dns, "google.com")
		if err != nil {
			continue
		}
		ip := strings.TrimSpace(strings.Split(string(out), "\n")[0])
		if net.ParseIP(ip) == nil {
			continue
		}
		working = append(working, dns)
	}
	return working
}

func (d *sporedrive) Displace(ctx context.Context, course course.Course) <-chan Progress {

	hyphae := course.Hyphae()

	// THE CLOUD'S SECRETS ARE SEALED ONCE PER DEPLOY, not once per host: every
	// host is handed the same bundle, so each writes the same version.
	seal := sync.OnceValues(d.sealSecrets)

	pCh := make(chan Progress, hyphae.Size()*1024)

	if cap(pCh) == 0 { // course is empty
		close(pCh)
		return pCh
	}

	go func() {
		dCtx, dCtxC := context.WithCancel(ctx)
		defer dCtxC()

		defer close(pCh)

		for _, hypha := range hyphae {
			errCh := hypha.Subnet.Run(dCtx, uint16(hypha.Concurrency), d.displaceHandler(hypha, pCh, seal))
			err := <-errCh
			if err != nil {
				// only errors in errCh
				// call it
				dCtxC()
				// forward
				pCh <- &progress{
					hypha:    hypha,
					host:     err.Host,
					stepName: "displacement",
					progress: 100,
					err:      err.Error,
				}
				// some jobs might still be running
				// let's make sure we wait until they all done
				for err := range errCh {
					pCh <- &progress{
						hypha:    hypha,
						host:     err.Host,
						stepName: "displacement",
						progress: 100,
						err:      err.Error,
					}
				}
				break
			}
		}
	}()

	return pCh
}

func updateResolvedConf(ctx context.Context, h remoteHost, resolvers []string) error {
	if len(resolvers) == 0 {
		return fmt.Errorf("no DNS resolvers provided")
	}

	file, err := h.Open("/etc/systemd/resolved.conf")
	if err != nil {
		return fmt.Errorf("failed to open /etc/systemd/resolved.conf: %w", err)
	}
	defer file.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, file); err != nil {
		return fmt.Errorf("failed to read /etc/systemd/resolved.conf: %w", err)
	}

	cfg, err := ini.Load(buf.Bytes())
	if err != nil {
		return fmt.Errorf("failed to parse /etc/systemd/resolved.conf: %w", err)
	}

	resolveSection := cfg.Section("Resolve")
	resolveSection.Key("DNS").SetValue(strings.Join(resolvers, " "))
	resolveSection.Key("DNSStubListener").SetValue("no")

	tmpFilePath := "/tmp/resolved.conf"
	tmpFile, err := h.OpenFile(tmpFilePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0640)
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer tmpFile.Close()

	if _, err := cfg.WriteTo(tmpFile); err != nil {
		return fmt.Errorf("failed to write modified configuration to temporary file: %w", err)
	}

	if _, err := h.Sudo(ctx, "cp", tmpFilePath, "/etc/systemd/resolved.conf"); err != nil {
		return fmt.Errorf("failed to copy modified configuration back to /etc/systemd/resolved.conf: %w", err)
	}

	if err := h.Remove(tmpFilePath); err != nil {
		return fmt.Errorf("failed to remove temporary file: %w", err)
	}

	return nil
}

func (d *sporedrive) uploadTau(ctx context.Context, h remoteHost, path string) (<-chan int, <-chan error, error) {
	rdr, err := preader.New(100*time.Millisecond, preader.WithContext(ctx), preader.WithBuffer(d.tauBinary), preader.Percentage())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read tau binary: %w", err)
	}

	f, err := h.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0750)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open %s: %w", path, err)
	}

	eCh := make(chan error, 1)

	go func() {
		defer f.Close()
		defer close(eCh)
		_, err = io.Copy(f, rdr)
		if err != nil {
			eCh <- fmt.Errorf("failed to upload tau: %w", err)
		}
	}()

	return rdr.ProgressChan(), eCh, nil
}

// relayUpload forwards an upload's progress and errors until the upload is
// done. eCh closes only after the copy returned, so every report the reader
// made is already buffered in pCh by then and is relayed before returning: a
// select that saw both ready could otherwise take the close first and drop it.
func relayUpload(ctx context.Context, pCh <-chan int, eCh <-chan error, progress func(int), fail func(error)) error {
	for {
		select {
		case err, ok := <-eCh:
			if !ok {
				for {
					select {
					case p, ok := <-pCh:
						if !ok {
							return nil
						}
						progress(p)
					default:
						return nil
					}
				}
			}
			fail(err)
		case p, ok := <-pCh:
			if !ok {
				pCh = nil
				continue
			}
			progress(p)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// uploadConfig writes a node's file to /tmp/<instance>.yaml. Its keys travel
// inline, as boot.File reads them, so there are no key files beside it.
func (d *sporedrive) uploadConfig(h remoteHost, inst instance) error {
	path := "/tmp/" + inst.name + ".yaml"
	f, err := h.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	e := yaml.NewEncoder(f)
	if err := e.Encode(inst.file); err != nil {
		return err
	}
	return e.Close()
}

// sealSecrets is the bundle every host running the secrets service is
// initialized with, marshaled for its stdin. It is nil when the cloud has no
// secret to hand over.
func (d *sporedrive) sealSecrets() ([]byte, error) {
	services, err := servicesOf(d.parser)
	if err != nil {
		return nil, err
	}
	bundle, err := services.bundle(d.now())
	if err != nil {
		return nil, err
	}
	if len(bundle.GetSecrets()) == 0 {
		return nil, nil
	}
	return proto.Marshal(bundle)
}

func (d *sporedrive) path(parts ...string) string {
	return path.Join(append([]string{d.root}, parts...)...)
}

func (d *sporedrive) binary() string { return d.path("bin", "tau") }

func (d *sporedrive) configOf(instance string) string {
	return d.path("config", instance+".yaml")
}

func (d *sporedrive) storageOf(instance string) string { return d.path("storage", instance) }

// moveAsideV1 keeps a v1 node's data out of the v2 node that takes its name.
// A directory whose config is not a v2 node file (one with a node: section)
// was left by something else, and is renamed rather than opened.
func (d *sporedrive) moveAsideV1(ctx context.Context, h remoteHost, instance string) error {
	storage := d.storageOf(instance)
	if _, err := h.Sudo(ctx, "test", "-d", storage); err != nil {
		return nil
	}
	if _, err := h.Sudo(ctx, "grep", "-qs", "^node:", d.configOf(instance)); err == nil {
		return nil
	}
	aside := storage + ".v1-" + d.now().UTC().Format("20060102T150405")
	if _, err := h.Sudo(ctx, "mv", storage, aside); err != nil {
		return fmt.Errorf("moving %s aside: %w", storage, err)
	}
	logger.Warnf("%s was not a v2 node's data and is now %s", storage, aside)
	return nil
}

// initSecrets streams the bundle to the node's `tau init`, on stdin, while it
// is stopped.
func (d *sporedrive) initSecrets(ctx context.Context, h remoteHost, inst instance, bundle []byte) error {
	out, err := h.SudoInput(ctx, bundle, d.binary(), "init", "--config", d.configOf(inst.name), "--root", d.storageOf(inst.name), "-")
	if err != nil {
		return fmt.Errorf("initializing tau@%s's secrets: %w: %s", inst.name, err, out)
	}
	return nil
}

func (d *sporedrive) uploadSystemdFile(h remoteHost) error {
	var cnf *ini.File

	if r, err := h.Open("/lib/systemd/system/tau@.service"); err == nil {
		cnf, err = ini.Load(r)
		if err != nil {
			return err
		}
	} else {
		cnf = ini.Empty()
	}

	unit := cnf.Section("Unit")
	unit.Key("Description").SetValue("Taubyte Tau Service Running %i")

	service := cnf.Section("Service")
	service.Key("Type").SetValue("simple")
	service.Key("ExecStartPre").SetValue("-/usr/sbin/setenforce 0")
	// EACH INSTANCE HAS ITS OWN ROOT: two nodes on one host must not share a
	// datastore.
	service.Key("ExecStart").SetValue(d.binary() + " start --config " + d.configOf("%i") + " --root " + d.storageOf("%i"))
	service.Key("StandardOutput").SetValue("journal")
	service.Key("User").SetValue("root")
	service.Key("Group").SetValue("root")
	service.Key("LimitAS").SetValue("infinity")
	service.Key("LimitRSS").SetValue("infinity")
	service.Key("LimitCORE").SetValue("infinity")
	service.Key("LimitNOFILE").SetValue("65536")
	service.Key("Restart").SetValue("always")
	service.Key("RestartSec").SetValue("1")

	install := cnf.Section("Install")
	install.Key("WantedBy").SetValue("multi-user.target")

	sdcnf, err := h.OpenFile("/tmp/tau@.service", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer sdcnf.Close()

	if _, err = cnf.WriteTo(sdcnf); err != nil {
		return err
	}

	return nil
}

func listTauInstances(ctx context.Context, h remoteHost) ([]string, error) {
	// Execute the systemctl command to list all tau@*.service units
	output, err := h.Sudo(ctx, "systemctl", "list-units", "--type=service", "--quiet", "--no-pager", "--all", "tau@*.service")
	if err != nil {
		return nil, fmt.Errorf("failed to list tau instances: %w", err)
	}

	// Convert the output from bytes to a string
	outputStr := string(output)

	// Split the output into lines
	lines := strings.Split(outputStr, "\n")

	var instances []string

	// Iterate over each line to extract service names
	for _, line := range lines {
		// Fields are separated by whitespace
		fields := strings.Fields(line)
		if len(fields) > 0 {
			unitName := fields[0] // The first field is the UNIT name
			if strings.HasPrefix(unitName, "tau@") && strings.HasSuffix(unitName, ".service") {
				instances = append(instances, strings.TrimSuffix(strings.TrimPrefix(unitName, "tau@"), ".service"))
			}
		}
	}

	return instances, nil
}

func (d *sporedrive) isSameTau(ctx context.Context, h remoteHost) bool {
	output, err := h.Execute(ctx, "md5sum", "-bz", d.binary())
	if err == nil && output != nil {
		fields := strings.Fields(string(output))
		if len(fields) > 1 && fields[0] == d.tauBinaryHash {
			return true
		}
	}
	return false
}

func defaultPortsToOpen() map[int]struct{} {
	return map[int]struct{}{
		53:  {},
		953: {},
		80:  {},
		443: {},
	}
}

func (d *sporedrive) displaceHandler(hypha *course.Hypha, progressCh chan<- Progress, seal func() ([]byte, error)) func(context.Context, host.Host) error {
	return func(ctx context.Context, h host.Host) error {
		updatingTau := (d.tauBinary != nil)

		pushProgress := func(name string, p int) {
			progressCh <- &progress{
				hypha:    hypha,
				host:     h,
				stepName: name,
				progress: p,
			}
		}

		pushError := func(name string, err error) error {
			progressCh <- &progress{
				hypha:    hypha,
				host:     h,
				stepName: name,
				progress: 100,
				err:      err,
			}
			return err
		}

		r, err := d.hostWrapper(ctx, h)
		if err != nil {
			return err
		}

		// Check for critical tools: systemctl and apt
		if _, err = r.Execute(ctx, "command", "-v", "systemctl"); err != nil {
			return pushError("dependencies", fmt.Errorf("systemctl is not installed: %w", err))
		}
		pushProgress("dependencies", 10)

		if _, err = r.Execute(ctx, "command", "-v", "apt"); err != nil {
			return pushError("dependencies", fmt.Errorf("apt is not installed: %w", err))
		}
		pushProgress("dependencies", 20)

		// Check and install Docker if not available
		if _, err = r.Execute(ctx, "command", "-v", "docker"); err != nil {
			// Docker is not installed, proceed to install
			_, err = r.Execute(ctx, "curl", "-fsSL", "https://get.docker.com", "-o", "/tmp/get-docker.sh")
			if err != nil {
				return pushError("dependencies", fmt.Errorf("failed to download Docker install script: %w", err))
			}
			pushProgress("dependencies", 25)

			_, err = r.Sudo(ctx, "sh", "/tmp/get-docker.sh")
			if err != nil {
				return pushError("dependencies", fmt.Errorf("failed to install Docker: %w", err))
			}
		}
		pushProgress("dependencies", 70)

		// Update package lists
		if _, err := r.Sudo(ctx, "apt-get", "update"); err != nil {
			return pushError("dependencies", fmt.Errorf("failed to update package lists: %w", err))
		}
		pushProgress("dependencies", 75)

		// Dependencies to be checked and installed if necessary
		i := 0
		for cmd, pkg := range map[string]string{
			"dig":     "dnsutils",
			"netstat": "net-tools",
		} {
			if _, err := r.Execute(ctx, "command", "-v", cmd); err != nil {
				// Command not found, install the package
				if _, err := r.Sudo(ctx, "apt-get", "install", "-y", pkg); err != nil {
					return pushError("dependencies", fmt.Errorf("failed to install %s: %w", pkg, err))
				}
			}
			i++
			pushProgress("dependencies", 55+i*5)
		}
		pushProgress("dependencies", 85)

		// Check for availability of ports 53 and 953
		netstatOutput, err := r.Sudo(ctx, "netstat", "-lnp")
		if err != nil {
			return pushError("dependencies", fmt.Errorf("failed to run netstat: %w", err))
		}

		// Probe candidate DNS resolvers from the host. We only keep the ones
		// that actually answer, so a host that can't reach (e.g.) 1.1.1.1
		// still gets a usable resolver list.
		workingResolvers := probeDNSResolvers(ctx, r)
		if len(workingResolvers) == 0 {
			return pushError("dependencies", fmt.Errorf("DNS resolution test failed: none of %v are reachable", dnsResolvers))
		}
		pushProgress("dependencies", 90)

		netstatStr := string(netstatOutput)
		if systemdResolvedRegexp.MatchString(netstatStr) {
			// systemd-resolved is using port 53, updating DNS settings using ini package
			if err := updateResolvedConf(ctx, r, workingResolvers); err != nil {
				return pushError("dependencies", fmt.Errorf("failed to update /etc/systemd/resolved.conf: %w", err))
			}

			// Restart systemd-resolved.service
			if _, err := r.Sudo(ctx, "systemctl", "restart", "systemd-resolved.service"); err != nil {
				return pushError("dependencies", fmt.Errorf("failed to restart systemd-resolved.service: %w", err))
			}

			// Update /etc/resolv.conf symlink
			if _, err := r.Sudo(ctx, "ln", "-sf", "/run/systemd/resolve/resolv.conf", "/etc/resolv.conf"); err != nil {
				return pushError("dependencies", fmt.Errorf("failed to update /etc/resolv.conf symlink: %w", err))
			}
		}
		pushProgress("dependencies", 100)

		pushProgress("checking state", 0)
		updatingTau = updatingTau && !d.isSameTau(ctx, r)
		pushProgress("checking state", 50)

		// TODO: check config changes

		pushProgress("checking state", 100)

		// Upload to /tmp
		if updatingTau {
			pushProgress("upload tau", 0)
			pCh, eCh, err := d.uploadTau(ctx, r, "/tmp/tau")
			if err != nil {
				return fmt.Errorf("failed to upload tau to /tmp: %w", err)
			}
			if err := relayUpload(ctx, pCh, eCh, func(p int) { pushProgress("upload tau", p) }, func(err error) { pushError("upload tau", err) }); err != nil {
				return pushError("upload tau", err)
			}

			pushProgress("upload tau", 100)
		}

		pushProgress("setup tau", 0)
		nodes, err := d.nodesFor(h.Name())
		if err != nil {
			return pushError("setup tau", fmt.Errorf("failed to plan nodes: %w", err))
		}
		for _, field := range nodes.ignored {
			logger.Warnf("%s: %s has no v2 equivalent and is not deployed", h.Name(), field)
		}
		desired := make([]string, len(nodes.instances))
		for i, inst := range nodes.instances {
			desired[i] = inst.name
		}

		existing, err := listTauInstances(ctx, r)
		if err != nil {
			return pushError("setup tau", fmt.Errorf("failed to list tau instances: %w", err))
		}

		// EVERY UNIT ALREADY THERE IS STOPPED: the host's node is redeployed
		// whole, and a v1 per-shape unit (tau@<shape>) is not a v2 node at all,
		// so it is also disabled.
		for _, inst := range existing {
			if _, err := r.Sudo(ctx, "systemctl", "stop", "tau@"+inst); err != nil {
				return pushError("setup tau", fmt.Errorf("failed to stop tau@%s: %w", inst, err))
			}
			if !slices.Contains(desired, inst) {
				if _, err := r.Sudo(ctx, "systemctl", "disable", "tau@"+inst); err != nil {
					return pushError("setup tau", fmt.Errorf("failed to disable tau@%s: %w", inst, err))
				}
			}
		}
		pushProgress("setup tau", 20)

		// The unit changed with v2, so it is written on every deploy and not
		// only with a new binary.
		if err = d.uploadSystemdFile(r); err != nil {
			return pushError("upload tau files", fmt.Errorf("failed to upload tau files to /tmp: %w", err))
		}
		if _, err = r.Sudo(ctx, "cp", "-f", "/tmp/tau@.service", "/lib/systemd/system/tau@.service"); err != nil {
			return pushError("upload tau files", fmt.Errorf("failed to copy tau@.service: %w", err))
		}
		if _, err = r.Sudo(ctx, "systemctl", "daemon-reload"); err != nil {
			return pushError("upload tau files", fmt.Errorf("failed to daemon-reload: %w", err))
		}

		if _, err = r.Sudo(ctx, "bash", "-c", "mkdir -p "+d.root+"/{bin,config,storage}"); err != nil {
			return pushError("setup tau", fmt.Errorf("failed create fs structure: %w", err))
		}
		pushProgress("setup tau", 25)

		for _, inst := range nodes.instances {
			if err := d.moveAsideV1(ctx, r, inst.name); err != nil {
				return pushError("setup tau", err)
			}
			if err := d.uploadConfig(r, inst); err != nil {
				return pushError("setup tau", fmt.Errorf("failed to create %s.yaml: %w", inst.name, err))
			}
			if _, err = r.Sudo(ctx, "cp", "-f", "/tmp/"+inst.name+".yaml", d.path("config")+"/"); err != nil {
				return pushError("setup tau", fmt.Errorf("failed to copy %s.yaml: %w", inst.name, err))
			}
		}
		pushProgress("setup tau", 30)

		if updatingTau {
			if _, err = r.Sudo(ctx, "cp", "-f", "/tmp/tau", d.path("bin")+"/"); err != nil {
				return pushError("setup tau", fmt.Errorf("failed to copy tau: %w", err))
			}
		}
		pushProgress("setup tau", 40)

		bundle, err := seal()
		if err != nil {
			return pushError("setup tau", fmt.Errorf("failed to seal the cloud's secrets: %w", err))
		}
		for _, inst := range nodes.instances {
			if inst.keepsSecrets() && bundle != nil {
				if err := d.initSecrets(ctx, r, inst, bundle); err != nil {
					return pushError("setup tau", err)
				}
			}
		}
		pushProgress("setup tau", 50)

		for _, inst := range desired {
			if !slices.Contains(existing, inst) {
				if _, err := r.Sudo(ctx, "systemctl", "enable", "tau@"+inst); err != nil {
					return pushError("setup tau", fmt.Errorf("failed to enable tau@%s: %w", inst, err))
				}
			}
			if _, err := r.Sudo(ctx, "systemctl", "start", "tau@"+inst); err != nil {
				return pushError("setup tau", fmt.Errorf("failed to start tau@%s: %w", inst, err))
			}
		}
		pushProgress("setup tau", 100)

		pushProgress("clean up", 0)
		r.Remove("/tmp/tau")
		r.Remove("/tmp/tau@.service")
		for _, inst := range desired {
			r.Remove("/tmp/" + inst + ".yaml")
		}
		pushProgress("clean up", 100)

		// Check if ufw is installed
		if _, err := r.Execute(ctx, "command", "-v", "ufw"); err == nil {
			pushProgress("firewall", 10)

			portsToCheck := defaultPortsToOpen()

			// Add each node's p2p port to the list of ports to check
			for _, inst := range nodes.instances {
				portsToCheck[inst.port] = struct{}{}
			}

			// Calculate progress increment
			totalPorts := len(portsToCheck)
			progressIncrement := 90 / totalPorts // 90 because we start at 10

			// Check and open each port if necessary
			currentProgress := 10
			for port := range portsToCheck {
				if _, err := r.Sudo(ctx, "ufw", "status", "numbered"); err == nil {
					if _, err := r.Sudo(ctx, "ufw", "status", "|", "grep", fmt.Sprintf("%d", port)); err != nil {
						if _, err := r.Sudo(ctx, "ufw", "allow", fmt.Sprintf("%d", port)); err != nil {
							return pushError("firewall", fmt.Errorf("failed to open port %d: %w", port, err))
						}
					}
				} else {
					return pushError("firewall", fmt.Errorf("failed to check ufw status: %w", err))
				}

				// Update progress
				currentProgress += progressIncrement
				pushProgress("firewall", currentProgress)
			}
			pushProgress("firewall", 100)
		}

		return nil
	}
}
