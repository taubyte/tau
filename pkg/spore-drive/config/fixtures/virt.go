package fixtures

import (
	"bytes"
	"io"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/spore-drive/config"
)

func VirtConfig() (afero.Fs, config.Parser) {
	cfs := afero.NewMemMapFs()
	p, _ := config.New(cfs, "/")
	p.Cloud().Domain().SetRoot("test.com")
	p.Cloud().Domain().SetGenerated("gtest.com")
	p.Cloud().Domain().Validation().Generate()

	p.Cloud().P2P().Swarm().Generate()

	p.Auth().Add("main").SetUsername("tau1")
	p.Auth().Add("main").SetPassword("testtest")

	p.Auth().Add("withkey").SetUsername("tau2")

	p.Auth().Add("withkey").SetKey("/keys/test.pem")
	privKeyData, _, _ := GenerateSSHKeyPair(256)
	privKeyFile, _ := p.Auth().Get("withkey").Create()
	io.Copy(privKeyFile, bytes.NewBuffer(privKeyData))
	privKeyFile.Close()

	p.Shapes().Shape("shape1").Services().Set("auth", "seer")
	p.Shapes().Shape("shape1").Ports().Set("main", 4242)
	p.Shapes().Shape("shape1").Ports().Set("lite", 4262)

	p.Shapes().Shape("shape2").Services().Set("gateway", "patrick", "monkey")
	p.Shapes().Shape("shape2").Ports().Set("main", 6242)
	p.Shapes().Shape("shape2").Ports().Set("lite", 6262)
	p.Shapes().Shape("shape2").Plugins().Set("plugin1@v0.1")

	host1 := p.Hosts().Host("host1")
	host1.Addresses().Add("1.2.3.4/24")
	host1.Addresses().Add("4.3.2.1/24")
	host1.SSH().SetFullAddress("1.2.3.4:4242")
	host1.SSH().Auth().Add("main")

	host1.SetLocation(1.25, 25.1)
	host1.Shapes().Instance("shape1").GenerateKey()
	host1.Shapes().Instance("shape2").GenerateKey()

	host2 := p.Hosts().Host("host2")
	host2.Addresses().Add("8.2.3.4/24")
	host2.Addresses().Add("4.3.2.8/24")
	host2.SSH().SetFullAddress("8.2.3.4:4242")
	host2.SSH().Auth().Add("withkey")

	host2.SetLocation(1.25, 25.1)
	host2.Shapes().Instance("shape1").GenerateKey()
	host2.Shapes().Instance("shape2").GenerateKey()

	p.Cloud().P2P().Bootstrap().Shape("shape1").Append("host2", "host1")
	p.Cloud().P2P().Bootstrap().Shape("shape2").Append("host2", "host1")

	p.Sync()

	return cfs, p
}
