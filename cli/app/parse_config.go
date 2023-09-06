package app

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/pnet"
	"github.com/taubyte/tau/config"
	"github.com/urfave/cli/v2"
	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v2"
)

// TODO: move to config as a methods

/* The function takes a context and a shape as input, then processes the configuration data stored in a YAML file.
This function constructs the necessary file paths based on a root directory and the shape parameter.
It reads and interprets the YAML data into a structure named src. Additionally, it performs adjustments to certain file paths and keys within the configuration.*/
func parseSourceConfig(ctx *cli.Context, shape string) (string, *config.Node, *config.Source, error) {
	
    	// Retrieve the 'root' path from the command line context or use the default if not specified.
	root := ctx.Path("root")
	if root == "" {
	        root = config.DefaultRoot
	    }
	
	// Check if 'root' is an absolute path; return an error if it's not.
	if !filepath.IsAbs(root) {
	        return "", nil, nil, fmt.Errorf("root folder `%s` is not absolute", root)
	    }
	
	// Define the path to the 'configRoot' folder by combining 'root' with "/config".
	configRoot := root + "/config"
	
	// Generate the configuration file path ('configPath') based on 'shape' and 'configRoot'.
	configPath := ctx.Path("path")
	if configPath == "" {
	        configPath = path.Join(configRoot, shape+".yaml")
	    }

	// Read the content of the configuration file specified by 'configPath'.
	data, err := os.ReadFile(configPath)
	if err != nil {
	        return "", nil, nil, fmt.Errorf("reading config file path `%s` failed with: %w", configPath, err)
	    }
	
	// ... Additional processing and parsing of 'data' ...
	
	// Return the results, including the constructed 'configPath' and any errors.
	// Placeholder, actual return values depend on further processing.


	// Create an empty 'config.Source' struct to hold the parsed YAML data.
	src := &config.Source{} 
	
	// Attempt to unmarshal the 'data' (YAML content) into the 'src' struct.
	if err = yaml.Unmarshal(data, &src); err != nil {
	    return "", nil, nil, fmt.Errorf("yaml unmarshal failed with: %w", err)
	}
	
	// Update the paths for private and public keys, and the 'Swarmkey' to use the 'configRoot' folder.
	// This ensures that these file paths are constructed relative to the 'configRoot' directory.
	src.Domains.Key.Private = path.Join(configRoot, src.Domains.Key.Private)
	
	// Check if a public key file path is provided in the configuration.
	if src.Domains.Key.Public != "" {
	    // If a public key path is provided, update it to use the 'configRoot' directory.
	    src.Domains.Key.Public = path.Join(configRoot, src.Domains.Key.Public)
	}
	
	// Update the 'Swarmkey' path to use the 'configRoot' directory.
	src.Swarmkey = path.Join(configRoot, src.Swarmkey)

	// Validate keys based on the provided protocols and key data.
	err = validateKeys(src.Protocols, src.Domains.Key.Private, src.Domains.Key.Public)
	if err != nil {
	    return "", nil, nil, err
	}
	
	// Create a 'config.Node' struct named 'protocol' with specific field values.
	protocol := &config.Node{
	    Root:            root,                               // Set the root directory.
	    Shape:           shape,                              // Set the shape.
	    P2PAnnounce:     src.P2PAnnounce,                    // Set P2P announce configuration.
	    P2PListen:       src.P2PListen,                      // Set P2P listen configuration.
	    Ports:           src.Ports.ToMap(),                  // Convert Ports to a map.
	    Location:        src.Location,                       // Set location configuration.
	    NetworkFqdn:     src.NetworkFqdn,                    // Set Network Fully Qualified Domain Name.
	    GeneratedDomain: src.Domains.Generated,              // Set the generated domain.
	    ServicesDomain:  convertToServiceRegex(src.NetworkFqdn), // Convert NetworkFqdn to service regex.
	    HttpListen:      "0.0.0.0:443",                      // Set HTTP listen address and port.
	    Protocols:       src.Protocols,                      // Set the protocols.
	    Plugins:         src.Plugins,                        // Set the plugins.
	    Peers:           src.Peers,                          // Set the peers.
	    DevMode:         ctx.Bool("dev-mode"),               // Set the development mode flag.
	}
	
	// Check if the 'src.Privatekey' is empty. If it is, return an error.
	if len(src.Privatekey) == 0 {
	    return "", nil, nil, errors.New("private key cannot be empty")
	}
	
	// Decode the 'src.Privatekey' from base64 encoding.
	base64Key, err := base64.StdEncoding.DecodeString(src.Privatekey)
	if err != nil {
	    return "", nil, nil, fmt.Errorf("converting private key to base 64 failed with: %s", err)
	}
	
	// Set the 'protocol.PrivateKey' field with the decoded private key.
	protocol.PrivateKey = []byte(base64Key)
	
	// Attempt to parse the 'Swarmkey' from the 'src.Swarmkey' value.
	// If there's an error during parsing, return the error.
	if protocol.SwarmKey, err = parseSwarmKey(src.Swarmkey); err != nil {
	    return "", nil, nil, err
	}
	
	// Attempt to parse domain validation keys (private and public keys) from 'src.Domains.Key'.
	// If there's an error during parsing, return the error.
	if protocol.DomainValidation, err = parseValidationKey(&src.Domains.Key); err != nil {
	    return "", nil, nil, err
	}


	pkey, err := crypto.UnmarshalPrivateKey(protocol.PrivateKey)
	if err != nil {
		return "", nil, nil, err
	}

	pid, err := peer.IDFromPublicKey(pkey.GetPublic())
	if err != nil {
		return "", nil, nil, err
	}

	return pid.Pretty(), protocol, src, nil
}

// Define a function named 'parseSwarmKey' that takes a 'filepath' as a parameter.
func parseSwarmKey(filepath string) (pnet.PSK, error) {
    // Check if the 'filepath' is not empty.
    if len(filepath) > 0 {
        // Read the content of the file specified by 'filepath'.
        data, err := os.ReadFile(filepath)
        if err != nil {
            // If there's an error during file reading, return nil for the PSK and an error with a description.
            return nil, fmt.Errorf("reading %s failed with: %w", filepath, err)
        }

        // Call the 'formatSwarmKey' function with the read 'data' to parse and format it as a PSK.
        return formatSwarmKey(string(data))
    }

    // If 'filepath' is empty, return nil for the PSK and no error (indicating no PSK available).
    return nil, nil
}

/*It extracts private and public key data from file paths. 
The private key is read from the specified path, while the public key can be sourced from either a 
provided public key file or generated from the private key. The parsed key data is returned in a 
structure that represents domain validation.*/
func parseValidationKey(key *config.DVKey) (config.DomainValidation, error) {
	// Private Key
	privateKey, err := os.ReadFile(key.Private)
	if err != nil {
		return config.DomainValidation{}, fmt.Errorf("reading private key `%s` failed with: %s", key.Private, err)
	}

	// Public Key
	var publicKey []byte
	if key.Public != "" {
		publicKey, err = os.ReadFile(key.Public)
		if err != nil {
			return config.DomainValidation{}, fmt.Errorf("reading public key `%s` failed with: %w", key.Public, err)
		}
	} else {
		publicKey, err = generatePublicKey(privateKey)
		if err != nil {
			return config.DomainValidation{}, fmt.Errorf("generating public key failed with: %w", err)
		}
	}

	return config.DomainValidation{PrivateKey: privateKey, PublicKey: publicKey}, nil
}

/*
1. Auth needs private key to start properly
2. Monkey/Substrate either need a public key or a private key to generate a public key from
*/
/*The validateKeys function serves to validate keys based on the protocols and key data provided. 
It ensures that specific keys are neither empty nor missing, based on the protocols being utilized. 
This is crucial for maintaining the integrity and security of network operations.*/
func validateKeys(protocols []string, privateKey, publicKey string) error {
	if slices.Contains(protocols, "auth") && privateKey == "" {
		return errors.New("domains private key cannot be empty when running auth")
	}

	for _, srv := range protocols {
		if (srv == "monkey" || srv == "substrate") && (privateKey == "" && publicKey == "") {
			return errors.New("domains public key cannot be empty when running monkey or node")
		}
	}

	return nil
}

func generatePublicKey(privateKey []byte) ([]byte, error) {
	block, _ := pem.Decode(privateKey)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, fmt.Errorf("failed to decode private key")
	}

	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key failed with: %w", err)
	}

	publicKeyDer, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return nil, fmt.Errorf("marshalling PKIX pub key failed with: %w", err)
	}
	pubKeyBlock := pem.Block{
		Type:    "PUBLIC KEY",
		Headers: nil,
		Bytes:   publicKeyDer,
	}

	pubKeyPem := pem.EncodeToMemory(&pubKeyBlock)

	return pubKeyPem, nil
}
