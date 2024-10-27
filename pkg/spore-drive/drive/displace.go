package drive

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/core/services/seer"
	preader "github.com/taubyte/tau/pkg/cli/io/progress"
	host "github.com/taubyte/tau/pkg/mycelium/host"
	"github.com/taubyte/tau/pkg/spore-drive/course"
	"gopkg.in/ini.v1"
	"gopkg.in/yaml.v3"
)

func (d *sporedrive) Displace(ctx context.Context, course course.Course) <-chan Progress {

	hyphae := course.Hyphae()

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
			errCh := hypha.Subnet.Run(dCtx, uint16(hypha.Concurrency), d.displaceHandler(hypha, pCh))
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

func updateResolvedConf(ctx context.Context, h remoteHost) error {
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
	resolveSection.Key("DNS").SetValue("1.1.1.1")
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

func (d *sporedrive) uploadPlugins(ctx context.Context, h remoteHost, dir string) error {
	// TODO
	return nil
}

func (d *sporedrive) writeSwarmKeyToTmp(h remoteHost) error {
	skr, err := d.parser.Cloud().P2P().Swarm().Open()
	if err != nil {
		return fmt.Errorf("failed to open swarm key: %w", err)
	}
	defer skr.Close()

	skf, err := h.OpenFile("/tmp/swarm.key", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open /tmp/swarm.key: %w", err)
	}
	defer skf.Close()

	_, err = io.Copy(skf, skr)

	return err
}

func (d *sporedrive) writeDomainPrivKeyToTmp(h remoteHost) error {
	pkr, err := d.parser.Cloud().Domain().Validation().OpenPrivateKey()
	if err != nil {
		return fmt.Errorf("failed to open private domain key: %w", err)
	}
	defer pkr.Close()

	pkf, err := h.OpenFile("/tmp/dv_private.key", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open /tmp/dv_private.key: %w", err)
	}
	defer pkf.Close()

	_, err = io.Copy(pkf, pkr)

	return err
}

func (d *sporedrive) writeDomainPubKeyToTmp(h remoteHost) error {
	pkr, err := d.parser.Cloud().Domain().Validation().OpenPublicKey()
	if err != nil {
		return fmt.Errorf("failed to open private domain key: %w", err)
	}
	defer pkr.Close()

	pkf, err := h.OpenFile("/tmp/dv_public.key", os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open /tmp/dv_public.key: %w", err)
	}
	defer pkf.Close()

	_, err = io.Copy(pkf, pkr)

	return err
}

func (d *sporedrive) writeConfig(h remoteHost, shape string, w io.Writer) error {
	hc := d.parser.Hosts().Host(h.Host().Name())
	hshape := hc.Shapes().Instance(shape)
	sc := d.parser.Shapes().Shape(shape)
	lat, lng := hc.Location()

	mainPort := int(sc.Ports().Get("main"))

	addrs := hc.Addresses().List()
	announce := make([]string, len(addrs))
	for i, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr)
		if err != nil {
			ip = net.ParseIP(addr)
			if ip == nil {
				return fmt.Errorf("`%s` is not valid IP or CIDR", addr)
			}
		}
		announce[i] = fmt.Sprintf("/ip4/%s/tcp/%d", ip.String(), mainPort)
	}

	bootstrap := make([]string, 0)
	for _, sh := range d.parser.Cloud().P2P().Bootstrap().List() {
		for _, ht := range d.parser.Cloud().P2P().Bootstrap().Shape(sh).List() {
			if ht == h.Host().Name() {
				continue
			}

			htc := d.parser.Hosts().Host(ht)
			bt := htc.Shapes().Instance(sh)
			shid := bt.Id()
			shMainPort := int(d.parser.Shapes().Shape(sh).Ports().Get("main"))
			for _, addr := range htc.Addresses().List() {
				ip, _, err := net.ParseCIDR(addr)
				if err != nil {
					ip = net.ParseIP(addr)
					if ip == nil {
						return fmt.Errorf("`%s` is not valid IP or CIDR", addr)
					}
				}
				bootstrap = append(bootstrap, fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s", ip.String(), shMainPort, shid))
			}
		}

	}

	e := yaml.NewEncoder(w)
	defer e.Close()

	err := e.Encode(config.Source{
		Privatekey:  hshape.Key(),
		Swarmkey:    "keys/swarm.key",
		Services:    sc.Services().List(),
		P2PListen:   []string{fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", mainPort)},
		P2PAnnounce: announce,
		Ports: config.Ports{
			Main: int(sc.Ports().Get("main")),
			Lite: int(sc.Ports().Get("lite")),
			Ipfs: int(sc.Ports().Get("ipfs")),
		},
		Location: &seer.Location{
			Latitude:  lat,
			Longitude: lng,
		},
		Peers:       bootstrap,
		NetworkFqdn: d.parser.Cloud().Domain().Root(),
		Domains: config.Domains{
			Key: config.DVKey{
				Private: "keys/dv_private.key",
				Public:  "keys/dv_public.key",
			},
			Generated: d.parser.Cloud().Domain().Generated(),
		},
	})
	if err != nil {
		return err
	}

	return nil
}

func (d *sporedrive) uploadConfig(h remoteHost, shape string) error {
	path := "/tmp/" + shape + ".yaml"
	f, err := h.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0750)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()

	if err = d.writeConfig(h, shape, f); err != nil {
		return err
	}

	if err = d.writeSwarmKeyToTmp(h); err != nil {
		return err
	}

	if err = d.writeDomainPrivKeyToTmp(h); err != nil {
		return err
	}

	if err = d.writeDomainPubKeyToTmp(h); err != nil {
		return err
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
	service.Key("ExecStart").SetValue("/tb/bin/tau start -s %i --root /tb")
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
	output, err := h.Execute(ctx, "md5sum", "-bz", "/tb/bin/tau")
	if err == nil && output != nil {
		fields := strings.Fields(string(output))
		if len(fields) > 1 && fields[0] == d.tauBinaryHash {
			return true
		}
	}
	return false
}

func (d *sporedrive) displaceHandler(hypha *course.Hypha, progressCh chan<- Progress) func(context.Context, host.Host) error {
	updatingTau := (d.tauBinary != nil)
	return func(ctx context.Context, h host.Host) error {
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

		if bytes.Contains(netstatOutput, []byte(":53 ")) && bytes.Contains(netstatOutput, []byte("systemd-resolve")) {
			// systemd-resolved is using port 53, updating DNS settings using ini package
			if err := updateResolvedConf(ctx, r); err != nil {
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
		pushProgress("dependencies", 90)

		// Validate DNS resolution using 1.1.1.1
		digOutput, err := r.Execute(ctx, "dig", "+short", "+timeout=5", "@1.1.1.1", "google.com")
		if err != nil {
			return pushError("dependencies", fmt.Errorf("failed to perform DNS resolution test: %w", err))
		}

		ip := strings.TrimSpace(strings.Split(string(digOutput), "\n")[0])
		if net.ParseIP(ip) == nil {
			return pushError("dependencies", fmt.Errorf("DNS resolution test failed, invalid IP: `%s`", ip))
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
			if pCh, eCh, err := d.uploadTau(ctx, r, "/tmp/tau"); err != nil {
				return fmt.Errorf("failed to upload tau to /tmp: %w", err)
			} else {
			tauBinaryUpload:
				for {
					select {
					case err, ok := <-eCh:
						if !ok {
							break tauBinaryUpload
						}
						pushError("upload tau", err)
					case pr, ok := <-pCh:
						if ok {
							pushProgress("upload tau", pr)
						}
					case <-ctx.Done():
						return pushError("upload tau", ctx.Err())
					}
				}
			}

			pushProgress("upload tau", 100)
		}

		pushProgress("upload plugins", 0)
		if err = d.uploadPlugins(ctx, r, "/tmp/tau-plugins"); err != nil {
			return pushError("upload plugins", fmt.Errorf("failed to upload tau plugins to /tmp: %w", err))
		}
		pushProgress("upload plugins", 100)

		pushProgress("setup tau", 0)
		allShapes, err := listTauInstances(ctx, r)
		if err != nil {
			return pushError("setup tau", fmt.Errorf("failed to list tau instances: %w", err))
		}

		hshapes := d.parser.Hosts().Host(h.Name()).Shapes().List()

		// stop tau instances and disable shapes that should be on the instance
		for _, shape := range append(allShapes, hypha.Shapes...) {
			if updatingTau || slices.Contains(hypha.Shapes, shape) || (slices.Contains(allShapes, shape) && !slices.Contains(hshapes, shape)) {
				if _, err := r.Sudo(ctx, "systemctl", "stop", "tau@"+shape); err != nil {
					return pushError("setup tau", fmt.Errorf("failed to stop tau@%s: %w", shape, err))
				}
			}

			if slices.Contains(allShapes, shape) && !slices.Contains(hshapes, shape) {
				if _, err := r.Sudo(ctx, "systemctl", "disable", "tau@"+shape); err != nil {
					return pushError("setup tau", fmt.Errorf("failed to disable tau@%s: %w", shape, err))
				}
				continue
			}
		}
		pushProgress("setup tau", 20)

		if updatingTau {
			// upload systemd and other files
			if err = d.uploadSystemdFile(r); err != nil {
				return pushError("upload tau files", fmt.Errorf("failed to upload tau files to /tmp: %w", err))
			}

			if _, err = r.Sudo(ctx, "cp", "-f", "/tmp/tau@.service", "/lib/systemd/system/tau@.service"); err != nil {
				return pushError("upload tau files", fmt.Errorf("failed to copy tau@.service: %w", err))
			}

			if _, err = r.Sudo(ctx, "systemctl", "daemon-reload"); err != nil {
				return pushError("upload tau files", fmt.Errorf("failed to daemon-reload: %w", err))
			}
		}

		if _, err = r.Sudo(ctx, "bash", "-c", "mkdir -p /tb/{bin,scripts,priv,cache,logs,storage,config/keys,plugins}"); err != nil {
			return pushError("setup tau", fmt.Errorf("failed create fs structure: %w", err))
		}

		pushProgress("setup tau", 25)

		for _, shape := range hypha.Shapes {
			if !slices.Contains(hshapes, shape) {
				continue
			}

			// upload config
			if err := d.uploadConfig(r, shape); err != nil {
				return pushError("setup tau", fmt.Errorf("failed to create %s.yaml: %w", shape, err))
			}

			if _, err = r.Sudo(ctx, "cp", "-f", "/tmp/"+shape+".yaml", "/tb/config/"); err != nil {
				return pushError("setup tau", fmt.Errorf("failed to copy %s.yaml: %w", shape, err))
			}

			if _, err = r.Sudo(ctx, "cp", "-f", "/tmp/swarm.key", "/tb/config/keys/"); err != nil {
				return pushError("setup tau", fmt.Errorf("failed to copy swarm.key: %w", err))
			}

			if _, err = r.Sudo(ctx, "cp", "-f", "/tmp/dv_private.key", "/tb/config/keys/"); err != nil {
				return pushError("setup tau", fmt.Errorf("failed to copy dv_private.key: %w", err))
			}

			if _, err = r.Sudo(ctx, "cp", "-f", "/tmp/dv_public.key", "/tb/config/keys/"); err != nil {
				return pushError("setup tau", fmt.Errorf("failed to copy dv_public.key: %w", err))
			}
		}
		pushProgress("setup tau", 30)

		if updatingTau {
			if _, err = r.Sudo(ctx, "cp", "-f", "/tmp/tau", "/tb/bin/"); err != nil {
				return pushError("setup tau", fmt.Errorf("failed to copy tau: %w", err))
			}
		}

		pushProgress("setup plugins", 0)
		/* TODO:
		- copy plugins
		- delete /tmp/plugins
		*/
		pushProgress("setup plugins", 100)

		pushProgress("setup tau", 50)
		// start tau
		for _, shape := range append(hshapes, hypha.Shapes...) {
			if !updatingTau && !slices.Contains(hypha.Shapes, shape) {
				continue
			}
			if !slices.Contains(allShapes, shape) {
				if _, err := r.Sudo(ctx, "systemctl", "enable", "tau@"+shape); err != nil {
					return pushError("setup tau", fmt.Errorf("failed to enable tau@%s: %w", shape, err))
				}
			}
			if _, err := r.Sudo(ctx, "systemctl", "start", "tau@"+shape); err != nil {
				return pushError("setup tau", fmt.Errorf("failed to start tau@%s: %w", shape, err))
			}
		}
		pushProgress("setup tau", 100)

		pushProgress("clean up", 0)
		r.Remove("/tmp/tau")
		r.Remove("/tmp/tau@.service")

		for _, shape := range hypha.Shapes {
			if !slices.Contains(hshapes, shape) {
				continue
			}

			r.Remove("/tmp/" + shape + ".yaml")
		}

		pushProgress("clean up", 100)

		return nil
	}
}
