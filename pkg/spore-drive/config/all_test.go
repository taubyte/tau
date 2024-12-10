package config

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"slices"
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

func assertDeepEqualSortedStrings(t *testing.T, a, b []string) {
	slices.Sort(a)
	slices.Sort(b)
	assert.DeepEqual(t, a, b)
}

func TestParserEdit(t *testing.T) {
	p, err := New(afero.NewMemMapFs(), "/")
	assert.NilError(t, err)

	assert.NilError(t, p.Cloud().Domain().SetRoot("test.com"))
	assert.NilError(t, p.Cloud().Domain().SetGenerated("gtest.com"))
	assert.NilError(t, p.Cloud().Domain().Validation().Generate())

	assert.NilError(t, p.Cloud().P2P().Swarm().Generate())

	assert.NilError(t, p.Auth().Add("main").SetUsername("tau"))
	assert.NilError(t, p.Auth().Add("main").SetPassword("testtest"))

	assert.NilError(t, p.Auth().Add("withkey").SetUsername("tau"))
	assert.NilError(t, p.Auth().Add("withkey").SetKey("keys/test.pem"))

	assert.NilError(t, p.Shapes().Shape("shape1").Services().Set("auth", "seer"))
	assert.NilError(t, p.Shapes().Shape("shape1").Ports().Set("main", 4242))
	assert.NilError(t, p.Shapes().Shape("shape1").Ports().Set("lite", 4262))

	assert.NilError(t, p.Shapes().Shape("shape2").Services().Set("gateway", "patrick", "monkey"))
	assert.NilError(t, p.Shapes().Shape("shape2").Ports().Set("main", 6242))
	assert.NilError(t, p.Shapes().Shape("shape2").Ports().Set("lite", 6262))
	assert.NilError(t, p.Shapes().Shape("shape2").Plugins().Set("plugin1@v0.1"))

	host1 := p.Hosts().Host("host1")
	assert.NilError(t, host1.Addresses().Add("1.2.3.4/24"))
	assert.NilError(t, host1.Addresses().Add("4.3.2.1/24"))
	assert.NilError(t, host1.SSH().SetFullAddress("1.2.3.4:4242"))
	assert.NilError(t, host1.SSH().Auth().Add("main"))

	assert.NilError(t, host1.SetLocation(1.25, 25.1))
	assert.NilError(t, host1.Shapes().Instance("shape1").GenerateKey())
	assert.NilError(t, host1.Shapes().Instance("shape2").GenerateKey())

	host2 := p.Hosts().Host("host2")
	assert.NilError(t, host2.Addresses().Add("8.2.3.4/24"))
	assert.NilError(t, host2.Addresses().Add("4.3.2.8/24"))
	assert.NilError(t, host2.SSH().SetFullAddress("8.2.3.4:4242"))
	assert.NilError(t, host2.SSH().Auth().Add("main"))

	assert.NilError(t, host2.SetLocation(1.25, 25.1))
	assert.NilError(t, host2.Shapes().Instance("shape1").GenerateKey())
	assert.NilError(t, host2.Shapes().Instance("shape2").GenerateKey())

	assert.NilError(t, p.Cloud().P2P().Bootstrap().Shape("shape1").Append("host1", "host2"))
	assert.NilError(t, p.Cloud().P2P().Bootstrap().Shape("shape2").Append("host1", "host2"))

	p.Sync()

}

func hashFile(r io.Reader) string {
	h := sha256.New()
	io.Copy(h, r)
	return hex.EncodeToString(h.Sum(nil))
}

func TestParserSchema(t *testing.T) {
	p, err := New(afero.NewBasePathFs(afero.NewOsFs(), "fixtures"), "/config")
	assert.NilError(t, err)

	assert.Equal(t, p.Cloud().Domain().Root(), "test.com")
	assert.Equal(t, p.Cloud().Domain().Generated(), "gtest.com")

	privDvKey, pubDvKey := p.Cloud().Domain().Validation().Keys()
	assert.Equal(t, privDvKey, "keys/dv_private.key")
	assert.Equal(t, pubDvKey, "keys/dv_public.key")

	privDvKeyFile, err := p.Cloud().Domain().Validation().OpenPrivateKey()
	assert.NilError(t, err)

	oPrivDvKeyFile, err := os.Open("fixtures/config/keys/dv_private.key")
	assert.NilError(t, err)

	assert.Equal(t, hashFile(privDvKeyFile), hashFile(oPrivDvKeyFile))

	pubDvKeyFile, err := p.Cloud().Domain().Validation().OpenPublicKey()
	assert.NilError(t, err)

	oPubDvKeyFile, err := os.Open("fixtures/config/keys/dv_public.key")
	assert.NilError(t, err)

	assert.Equal(t, hashFile(pubDvKeyFile), hashFile(oPubDvKeyFile))

	assert.Equal(t, p.Cloud().P2P().Swarm().Get(), "keys/swarm.key")
	swarmKeyFile, err := p.Cloud().P2P().Swarm().Open()
	assert.NilError(t, err)

	oSwarmKeyFile, err := os.Open("fixtures/config/keys/swarm.key")
	assert.NilError(t, err)

	assert.Equal(t, hashFile(swarmKeyFile), hashFile(oSwarmKeyFile))

	assert.Equal(t, p.Auth().Get("main").Username(), "tau")
	assert.Equal(t, p.Auth().Get("main").Password(), "testtest")
	assert.Equal(t, p.Auth().Get("main").Key(), "")

	_, err = p.Auth().Get("main").Open()
	assert.Error(t, err, "no key found")

	assert.Equal(t, p.Auth().Get("withkey").Username(), "tau")
	assert.Equal(t, p.Auth().Get("withkey").Password(), "")
	assert.Equal(t, p.Auth().Get("withkey").Key(), "keys/test.pem")

	sshKeyFile, err := p.Auth().Get("withkey").Open()
	assert.NilError(t, err)

	oSshKeyFile, err := os.Open("fixtures/config/keys/test.pem")
	assert.NilError(t, err)

	assert.Equal(t, hashFile(sshKeyFile), hashFile(oSshKeyFile))

	assertDeepEqualSortedStrings(t, p.Shapes().List(), []string{"shape1", "shape2"})

	assert.DeepEqual(t, p.Shapes().Shape("shape1").Services().List(), []string{"auth", "seer"})
	assert.Equal(t, p.Shapes().Shape("shape1").Ports().Get("main"), uint16(4242))
	assert.Equal(t, p.Shapes().Shape("shape1").Ports().Get("lite"), uint16(4262))

	assertDeepEqualSortedStrings(t, p.Shapes().Shape("shape1").Ports().List(), []string{"main", "lite"})

	assert.DeepEqual(t, p.Shapes().Shape("shape2").Services().List(), []string{"gateway", "patrick", "monkey"})
	assert.Equal(t, p.Shapes().Shape("shape2").Ports().Get("main"), uint16(6242))
	assert.Equal(t, p.Shapes().Shape("shape2").Ports().Get("lite"), uint16(6262))
	assert.DeepEqual(t, p.Shapes().Shape("shape2").Plugins().List(), []string{"plugin1@v0.1"})

	host1 := p.Hosts().Host("host1")
	assertDeepEqualSortedStrings(t, host1.Addresses().List(), []string{"1.2.3.4/24", "4.3.2.1/24"})
	assert.Equal(t, host1.SSH().Address(), "1.2.3.4")
	assert.Equal(t, host1.SSH().Port(), uint16(4242))
	assert.DeepEqual(t, host1.SSH().Auth().List(), []string{"main"})

	for _, s := range []string{"shape1", "shape2"} {
		assert.Assert(t, slices.Contains(host1.Shapes().List(), s))
	}

	lat, lng := host1.Location()
	assert.Equal(t, lat, float32(1.25))
	assert.Equal(t, lng, float32(25.1))

	assert.Equal(t, host1.Shapes().Instance("shape1").Key(), "CAESQIWC2KRhsEexLpN4DsJwki4S56IN5IreCANf89+F+OpTWn7Tf+RwZnUbiZYdxsTFrbBJQ9S+A0oFp8a1SSAN2EE=")
	assert.Equal(t, host1.Shapes().Instance("shape1").Id(), "12D3KooWFud2Y9UuRW6Si2whRoyu9AXJLEgFed6xhabsyVe8pHck")

	assert.Equal(t, host1.Shapes().Instance("shape2").Key(), "CAESQHLGyFbnI2GP7e3Gib9ut7IFDxrkbTbs7LFAJYhe0w0LXEtYrH7HyODglOFY3oXQ+kCfoFcvqvZnAD6K5UavO2c=")
	assert.Equal(t, host1.Shapes().Instance("shape2").Id(), "12D3KooWG2eK9dVPazdxSF6eDS1ESgD5N3n2xfsGDKaZuifuE57t")

	host2 := p.Hosts().Host("host2")
	assertDeepEqualSortedStrings(t, host2.Addresses().List(), []string{"8.2.3.4/24", "4.3.2.8/24"})
	assert.Equal(t, host2.SSH().Address(), "8.2.3.4")
	assert.Equal(t, host2.SSH().Port(), uint16(4242))
	assert.DeepEqual(t, host2.SSH().Auth().List(), []string{"main"})

	lat, lng = host2.Location()
	assert.Equal(t, lat, float32(1.25))
	assert.Equal(t, lng, float32(25.1))

	assert.Equal(t, host2.Shapes().Instance("shape1").Key(), "CAESQDpF3eQuEbGsjSRkf3uE6E4SV3dvwSSMUcNJkimOUc0hO6gPoZjsq/NO/FwVz8FoZ4LG/5DSF2B/Rl+vJCNLlUI=")
	assert.Equal(t, host2.Shapes().Instance("shape1").Id(), "12D3KooWDqErfJk5kUTfpAcYSPwr4nztSHkDqVZjC1wpkJ9EDsDK")

	assert.Equal(t, host2.Shapes().Instance("shape2").Key(), "CAESQIA03gtBTeL8eYNQKcJ+VqKLgarHfofd5I/CV/zEsxHiqfihV9ZXjl0qtaTPEWExBgqRn+w2YLD6FQy8zBdEabI=")
	assert.Equal(t, host2.Shapes().Instance("shape2").Id(), "12D3KooWMFrxcHhw2gvnp8iBVTcqZ3f1B4m5W1vpGoi6q5jxv1Ms")
}

func TestParserDelAppendClear(t *testing.T) {
	p, err := New(afero.NewBasePathFs(afero.NewOsFs(), "fixtures"), "/config")
	assert.NilError(t, err)

	assertDeepEqualSortedStrings(t, p.Auth().List(), []string{"main", "withkey"})
	assert.NilError(t, p.Auth().Delete("main"))
	assert.DeepEqual(t, p.Auth().List(), []string{"withkey"})

	assert.NilError(t, p.Hosts().Host("host1").Shapes().Delete("shape1"))
	assert.DeepEqual(t, p.Hosts().Host("host1").Shapes().List(), []string{"shape2"})
	assert.Equal(t, p.Hosts().Host("host1").Shapes().Instance("shape1").Id(), "")

	assert.NilError(t, p.Hosts().Delete("host1"))
	assert.DeepEqual(t, p.Hosts().List(), []string{"host2"})

	assert.NilError(t, p.Hosts().Host("host2").Addresses().Delete("8.2.3.4/24"))
	assert.DeepEqual(t, p.Hosts().Host("host2").Addresses().List(), []string{"4.3.2.8/24"})

	assert.NilError(t, p.Hosts().Host("host2").Addresses().Append("8.2.3.4/24"))
	assert.DeepEqual(t, p.Hosts().Host("host2").Addresses().List(), []string{"4.3.2.8/24", "8.2.3.4/24"})
	assert.NilError(t, p.Hosts().Host("host2").Addresses().Clear())
	assert.DeepEqual(t, p.Hosts().Host("host2").Addresses().List(), []string{})

	assert.NilError(t, p.Shapes().Shape("shape2").Ports().Delete("lite"))
	assert.DeepEqual(t, p.Shapes().Shape("shape2").Ports().List(), []string{"main"})

	assert.NilError(t, p.Shapes().Delete("shape2"))
	assert.DeepEqual(t, p.Shapes().List(), []string{"shape1"})
}
