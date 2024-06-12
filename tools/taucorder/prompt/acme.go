package prompt

import (
	"errors"
	"fmt"
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

	fmt.Println(string(_pem))
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

	fmt.Printf("\nCertificate:\n%#v", cert)
	return nil
}
