package service

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	cr "bitbucket.org/taubyte/p2p/streams/command/response"
	"github.com/taubyte/go-interfaces/p2p/streams"
	"github.com/taubyte/utils/maps"
)

// ErrCacheMiss is returned when a certificate is not found in cache.
var ErrCacheMiss = errors.New("acme/autocert: certificate cache miss")

// https://golang.org/pkg/crypto/x509/#example_Certificate_Verify

// TODO: make sure we verify expiration
func (srv *AuthService) x509Validate(fqdn string, certificate []byte) error {
	logger.Debug(fmt.Sprintf("Validating certificate for `%s`", fqdn))
	defer logger.Debug(fmt.Sprintf("Validating certificate for `%s` done", fqdn))
	block, _ := pem.Decode(certificate)
	if block == nil {
		return errors.New("failed to parse certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return errors.New("Failed to parse certificate: " + err.Error())
	}

	opts := x509.VerifyOptions{
		DNSName: fqdn,
	}

	if _, err := cert.Verify(opts); err != nil {
		return errors.New("Failed to verify certificate: " + err.Error())
	}

	return nil
}

// TODO: validate fqdn
func (srv *AuthService) setACMECertificate(ctx context.Context, fqdn string, certificate []byte) error {
	logger.Debug(fmt.Sprintf("Set certificate for `%s`", fqdn))
	defer logger.Debug(fmt.Sprintf("Set certificate for `%s` done", fqdn))

	/*err := srv.x509Validate(fqdn, certificate)
	if err != nil {
		return err
	}*/ // TODO add later

	err := srv.db.Put(ctx, "/acme/"+base64.StdEncoding.EncodeToString([]byte(fqdn))+"/certificate/pem", certificate)
	if err != nil {
		logger.Error(fmt.Sprintf("Set certificate for `%s` failed: %s", fqdn, err.Error()))
		return err
	}

	logger.Debug(fmt.Sprintf("Set certificate for `%s` = %v", fqdn, certificate))

	return nil
}

func (srv *AuthService) setACMEStaticCertificate(ctx context.Context, fqdn string, certificate []byte) error {
	logger.Debug(fmt.Sprintf("Set certificate for `%s`", fqdn))
	defer logger.Debug(fmt.Sprintf("Set certificate for `%s` done", fqdn))

	err := srv.db.Put(ctx, "/static/"+base64.StdEncoding.EncodeToString([]byte(fqdn))+"/certificate/pem", certificate)
	if err != nil {
		logger.Error(fmt.Sprintf("Set certificate for `%s` failed: %s", fqdn, err.Error()))
		return fmt.Errorf("failed setting static certificate with %v", err)
	}

	logger.Debug(fmt.Sprintf("Set certificate for `%s` = %v", fqdn, certificate))

	return nil
}

// TODO: validate fqdn
// LATER: validate peer has access to it
func (srv *AuthService) getACMECertificate(ctx context.Context, fqdn string) ([]byte, error) {
	logger.Debug(fmt.Sprintf("Get certificate for `%s`", fqdn))
	defer logger.Debug(fmt.Sprintf("Get certificate for `%s` done", fqdn))

	key := "/acme/" + base64.StdEncoding.EncodeToString([]byte(fqdn)) + "/certificate/pem"
	certificate, err := srv.db.Get(ctx, key)
	if err != nil {
		certificate, err = srv.getACMEStaticCertificate(ctx, fqdn)
		if err != nil {
			logger.Error("Get certificate for " + fqdn + " returned " + err.Error())
			return nil, ErrCacheMiss
		}
	}

	if certificate == nil {
		// cleanup entry
		logger.Error(fqdn + " : Found empty certificate!")
		srv.db.Delete(ctx, key)
		return nil, ErrCacheMiss //errors.New("Found empty certificate!")
	}

	// double check that the certificate in store is valid
	// just in case it expired or was corrupted
	/*err = srv.x509Validate(fqdn, certificate)
	if err != nil {
		// clean-up entry
		logger.Error(fqdn, " : ", err))
		srv.db.Delete(key)
		return nil, ErrCacheMiss //err
	}*/ // TODO: re-add later

	logger.Debug(fmt.Sprintf("Get certificate for `%s`: %v", fqdn, certificate))

	return certificate, nil
}

func (srv *AuthService) getACMEStaticCertificate(ctx context.Context, fqdn string) ([]byte, error) {
	logger.Debug(fmt.Sprintf("Get certificate for `%s`", fqdn))
	defer logger.Debug(fmt.Sprintf("Get certificate for `%s` done", fqdn))

	key := "/static/" + base64.StdEncoding.EncodeToString([]byte(fqdn)) + "/certificate/pem"
	certificate, err := srv.db.Get(ctx, key)
	if err != nil {
		wildCard := generateWildCardDomain(fqdn)
		key := "/static/" + base64.StdEncoding.EncodeToString([]byte(wildCard)) + "/certificate/pem"
		certificate, err = srv.db.Get(ctx, key)
		if err != nil {
			logger.Error("Get certificate for " + fqdn + " returned " + err.Error())
			return nil, ErrCacheMiss
		}
	}

	if certificate == nil {
		// cleanup entry
		logger.Error(fqdn + " : Found empty certificate!")
		srv.db.Delete(ctx, key)
		return nil, ErrCacheMiss
	}

	logger.Debug(fmt.Sprintf("Get certificate for `%s`: %v", fqdn, certificate))

	return certificate, nil
}

// add a proces to clean-up
func (srv *AuthService) getACMECache(ctx context.Context, key string) ([]byte, error) {
	logger.Debug(fmt.Sprintf("Get acme cache for `%s`", key))
	defer logger.Debug(fmt.Sprintf("Get acme cache for `%s` done", key))

	key_base := "/acme/cache/" + base64.StdEncoding.EncodeToString([]byte(key))
	data, err := srv.db.Get(ctx, key_base+"/data")
	if err != nil {
		return nil, ErrCacheMiss
	}

	if data == nil {
		logger.Error(key + " : Found empty !")
		srv.db.Delete(ctx, key_base+"/data")
		srv.db.Delete(ctx, key_base+"/timestamp")
		return nil, ErrCacheMiss
	}

	logger.Debug(fmt.Sprintf("Get acme cache for `%s`: %v", key, data))

	return data, nil
}

// add a GC to clean up data
func (srv *AuthService) setACMECache(ctx context.Context, key string, data []byte) error {
	logger.Debug(fmt.Sprintf("Set acme cache for `%s`", key))
	defer logger.Debug(fmt.Sprintf("Set acme cache for `%s` done", key))

	key_base := "/acme/cache/" + base64.StdEncoding.EncodeToString([]byte(key))
	err := srv.db.Put(ctx, key_base+"/data", data)
	if err != nil {
		return err
	}

	err = srv.db.Put(ctx, key_base+"/timestamp", []byte(fmt.Sprintf("%d", time.Now().Unix())))
	if err != nil {
		srv.db.Delete(ctx, key_base+"/data")
		return err
	}

	return nil
}

func (srv *AuthService) deleteACMECache(ctx context.Context, key string) error {
	logger.Debug(fmt.Sprintf("Del acme cache for `%s`", key))
	defer logger.Debug(fmt.Sprintf("Del acme cache for `%s` done", key))

	key_base := "/acme/cache/" + base64.StdEncoding.EncodeToString([]byte(key))
	err := srv.db.Delete(ctx, key_base+"/data")
	if err != nil {
		return err
	}

	srv.db.Delete(ctx, key_base+"/timestamp")

	return nil
}

func (srv *AuthService) acmeServiceHandler(ctx context.Context, st streams.Connection, body streams.Body) (cr.Response, error) {
	// params:
	//  TODO: add encrption key to service library
	//  action: get/set
	//  fqdn: domain name
	action, err := maps.String(body, "action")
	if err != nil {
		return nil, err
	}

	switch action {
	case "get":
		fqdn, err := maps.String(body, "fqdn")
		if err != nil {
			return nil, err
		}
		certificate, err := srv.getACMECertificate(ctx, fqdn)
		if err != nil {
			return nil, err
		}
		return cr.Response{"certificate": certificate}, nil
	case "get-static":
		fqdn, err := maps.String(body, "fqdn")
		if err != nil {
			return nil, fmt.Errorf("failed maps string in get-static %v", err)
		}
		certificate, err := srv.getACMEStaticCertificate(ctx, fqdn)
		if err != nil {
			return nil, fmt.Errorf("failed getACMEStaticCertificate with %v", err)
		}
		return cr.Response{"certificate": certificate}, nil
	case "set":
		fqdn, err := maps.String(body, "fqdn")
		if err != nil {
			return nil, err
		}
		certificate, err := maps.ByteArray(body, "certificate")
		if err != nil {
			return nil, err
		}
		return nil, srv.setACMECertificate(ctx, fqdn, certificate)
	case "set-static":
		fqdn, err := maps.String(body, "fqdn")
		if err != nil {
			return nil, fmt.Errorf("failed maps string in set-static %v", err)
		}
		certificate, err := maps.ByteArray(body, "certificate")
		if err != nil {
			return nil, fmt.Errorf("failed maps ByteArray in set-static with %v", err)
		}
		return nil, srv.setACMEStaticCertificate(ctx, fqdn, certificate)
	case "cache-get":
		key, err := maps.String(body, "key")
		if err != nil {
			return nil, err
		}
		data, err := srv.getACMECache(ctx, key)
		if err != nil {
			return nil, err
		}
		return cr.Response{"data": data}, nil
	case "cache-set":
		key, err := maps.String(body, "key")
		if err != nil {
			return nil, err
		}
		data, err := maps.ByteArray(body, "data")
		if err != nil {
			return nil, err
		}
		return nil, srv.setACMECache(ctx, key, data)
	case "cache-delete":
		key, err := maps.String(body, "key")
		if err != nil {
			return nil, err
		}
		err = srv.deleteACMECache(ctx, key)
		if err != nil {
			return nil, err
		}
		return nil, nil
	default:
		return nil, errors.New("Acme action `" + action + "` not reconized.")
	}
}
