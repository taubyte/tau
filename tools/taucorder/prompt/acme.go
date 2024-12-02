package prompt

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"

	goPrompt "github.com/c-bata/go-prompt"
)

var acmeTree = &tctree{
	leafs: []*leaf{
		exitLeaf,
		{
			validator: stringValidator("injectStaticCert"),
			ret: []goPrompt.Suggest{
				{
					Text:        "injectStaticCert",
					Description: "inject a certificate into auth. Ex: inject domainName certFile",
				},
			},
			handler: injectStaticCert,
		},
		{
			validator: stringValidator("getCert"),
			ret: []goPrompt.Suggest{
				{
					Text:        "getCert",
					Description: "gets the certificate file for a domain in /acme",
				},
			},
			handler: getCertificate,
		},
		{
			validator: stringValidator("getStaticCert"),
			ret: []goPrompt.Suggest{
				{
					Text:        "getStaticCert",
					Description: "gets the certificate file for a domain in /static",
				},
			},
			handler: getStaticCertificate,
		},
	},
}

func injectStaticCert(p Prompt, args []string) error {
	if len(args) != 3 {
		fmt.Println("Must provide a domain and certificate file")
		return errors.New("must provide an domain and certificate file")
	}

	fileBytes, err := os.ReadFile(args[2])
	if err != nil {
		fmt.Println("Failed reading file with ", err)
		return fmt.Errorf("failed reading file with %v", err)
	}

	err = p.AuthClient().InjectStaticCertificate(args[1], fileBytes)
	if err != nil {
		fmt.Printf("Failed injecting certificate for %s with %v\n", args[1], err)
		return fmt.Errorf(" Failed injecting certificate for %s with %v", args[1], err)
	}

	fmt.Printf("Successfully injected certificate for %s\n", args[1])
	return nil
}

func getCertificate(p Prompt, args []string) error {
	if len(args) != 2 {
		fmt.Println("Must provide an domain")
		return errors.New("must provide an domain and key file")
	}

	_pem, err := p.AuthClient().GetCertificate(args[1])
	if err != nil {
		fmt.Printf("Failed getting certificate for %s with %v\n", args[1], err)
		return fmt.Errorf(" Failed getting certificate for %s with %v", args[1], err)
	}

	fmt.Println(_pem)
	return nil
}

func getStaticCertificate(p Prompt, args []string) error {
	if len(args) != 2 {
		fmt.Println("Must provide an domain")
		return errors.New("must provide an domain and key file")
	}

	cert, err := p.AuthClient().GetStaticCertificate(args[1])
	if err != nil {
		fmt.Printf("Failed getting certificate for %s with %v\n", args[1], err)
		return fmt.Errorf(" Failed getting certificate for %s with %v", args[1], err)
	}

	prettyPrintTLSCertificate(cert)

	return nil
}

func prettyPrintTLSCertificate(cert *tls.Certificate) {
	fmt.Println("TLS Certificate Details:")

	for i, certData := range cert.Certificate {
		fmt.Printf("\nCertificate %d:\n", i+1)
		block := &pem.Block{
			Type:  "CERTIFICATE",
			Bytes: certData,
		}
		fmt.Println(string(pem.EncodeToMemory(block)))

		parsedCert, err := x509.ParseCertificate(certData)
		if err != nil {
			log.Printf("Error parsing certificate %d: %v", i+1, err)
			continue
		}
		fmt.Printf("  Subject: %s\n", parsedCert.Subject)
		fmt.Printf("  Issuer: %s\n", parsedCert.Issuer)
		fmt.Printf("  Valid From: %s\n", parsedCert.NotBefore)
		fmt.Printf("  Valid Until: %s\n", parsedCert.NotAfter)
		if len(parsedCert.DNSNames) > 0 {
			fmt.Printf("  DNS Names: %v\n", parsedCert.DNSNames)
		}
	}

	if cert.PrivateKey != nil {
		fmt.Println("\nPrivate Key:")
		switch key := cert.PrivateKey.(type) {
		case *rsa.PrivateKey:
			privKeyBytes := x509.MarshalPKCS1PrivateKey(key)
			privKeyPem := pem.EncodeToMemory(&pem.Block{
				Type:  "RSA PRIVATE KEY",
				Bytes: privKeyBytes,
			})
			fmt.Println(string(privKeyPem))
		case *ecdsa.PrivateKey:
			privKeyBytes, err := x509.MarshalECPrivateKey(key)
			if err != nil {
				log.Printf("Failed to marshal ECDSA private key: %v", err)
				return
			}
			privKeyPem := pem.EncodeToMemory(&pem.Block{
				Type:  "EC PRIVATE KEY",
				Bytes: privKeyBytes,
			})
			fmt.Println(string(privKeyPem))
		case ed25519.PrivateKey:
			privKeyBytes, err := x509.MarshalPKCS8PrivateKey(key)
			if err != nil {
				log.Printf("Failed to marshal Ed25519 private key: %v", err)
				return
			}
			privKeyPem := pem.EncodeToMemory(&pem.Block{
				Type:  "PRIVATE KEY",
				Bytes: privKeyBytes,
			})
			fmt.Println(string(privKeyPem))
		default:
			fmt.Println("Unknown private key type.")
		}
	} else {
		fmt.Println("\nNo private key found in the TLS certificate.")
	}
}
