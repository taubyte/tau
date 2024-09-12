package vm

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/hashicorp/go-plugin"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-orbit/link"
)

func (p *vmPlugin) connect() (err error) {
	p.proc = plugin.NewClient(
		&plugin.ClientConfig{
			HandshakeConfig: HandShake(),
			Plugins:         link.ClientPluginMap,
			Cmd:             exec.Command(p.filename),
			AllowedProtocols: []plugin.Protocol{
				plugin.ProtocolGRPC,
			},
		},
	)

	if p.client, err = p.proc.Client(); err != nil {
		return fmt.Errorf("getting rpc protocol client failed with: %w", err)
	}

	return
}

func (p *vmPlugin) reconnect() error {
	p.proc.Kill()
	return p.connect()
}

func (p *vmPlugin) waitTillCopyIsDone() error {
	var size int64 = -1
	for {
		select {
		case <-p.ctx.Done():
			return errors.New("context done")
		case <-time.After(3 * time.Second):
			info, err := os.Stat(p.origin)
			if err == nil {
				if info.Size() != size {
					size = info.Size()
				} else {
					return nil
				}
			}
		}
	}
}

func (p *vmPlugin) watch() error {
	// will panic if any error
	// creates a new file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	go func() {
		defer watcher.Close()
		for {
			select {
			case <-p.ctx.Done():
				return
			case <-time.After(ProcWatchInterval):
				if p.proc.Exited() {
					if err := p.reload(); err != nil {
						log.Println("reloading exited plugin error" + err.Error())
					}
				}
			case event := <-watcher.Events:
				if event.Name == p.origin && (event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Create == fsnotify.Create) {
					if err = p.waitTillCopyIsDone(); err != nil {
						log.Println("copy done error:" + err.Error())
						return
					}

					if err := p.reload(); err != nil {
						log.Println("reload error" + err.Error())
					}

					subsHandle(p.origin)
				}
			case err := <-watcher.Errors:
				log.Println("error:", err)
			}
		}
	}()

	dir := filepath.Dir(p.origin)
	if err = watcher.Add(dir); err != nil {
		return fmt.Errorf("adding fs watcher on `%s` failed with: %w", p.origin, err)
	}

	return nil
}

func (p *vmPlugin) new(instance vm.Instance) (*pluginInstance, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	pI := &pluginInstance{
		plugin:   p,
		instance: instance,
	}

	var err error
	if pI.satellite, err = p.getLink(); err != nil {
		return nil, fmt.Errorf("getting link to satelite failed with: %w", err)
	}

	meta, err := pI.satellite.Meta(p.ctx)
	if err != nil {
		return nil, fmt.Errorf("meta failed with: %w", err)
	}

	if len(p.name) < 1 {
		p.name = meta.Name
	}

	return pI, nil
}

func (p *vmPlugin) getLink() (sat Satellite, err error) {
	raw, err := p.client.Dispense("satellite")
	if err != nil {
		return nil, fmt.Errorf("getting satellite failed with: %w", err)
	}

	if sat, _ = raw.(Satellite); sat == nil {
		return nil, errors.New("satellite is not a plugin")
	}

	return sat, nil
}

func (p *vmPlugin) reload() (err error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if err = p.prepFile(); err != nil {
		return fmt.Errorf("prepping `%s` failed with: %w", p.name, err)
	}

	for pI := range p.instances {
		if err := pI.cleanup(); err != nil {
			return fmt.Errorf("cleanup plugin `%s` failed with: %w", p.name, err)
		}
	}

	if err := p.reconnect(); err != nil {
		return fmt.Errorf("reconnecting plugin `%s` failed with: %w", p.name, err)
	}

	for pI := range p.instances {
		if err := pI.reload(); err != nil {
			return fmt.Errorf("reloading plugin `%s` failed with: %w", p.name, err)
		}
	}

	if err = p.hashFile(); err != nil {
		return fmt.Errorf("hashing `%s` failed with: %w", p.name, err)
	}

	return nil
}
