package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	pb "github.com/taubyte/tau/pkg/spore-drive/config/proto/go"

	"github.com/taubyte/tau/pkg/spore-drive/config"
)

func (s *Service) doCloud(in *pb.Cloud, p config.Parser) (*pb.Return, error) {
	if a := in.GetDomain(); a != nil {
		// get
		if x := a.GetRoot(); x != nil && x.GetGet() {
			return returnString(p.Cloud().Domain().Root()), nil
		}

		if x := a.GetGenerated(); x != nil && x.GetGet() {
			return returnString(p.Cloud().Domain().Generated()), nil
		}

		if x := a.GetValidation(); x != nil && x.GetKeys() != nil {
			z := x.GetKeys()
			if k := z.GetPath(); k != nil {
				if l := k.GetPrivateKey(); l != nil && l.GetGet() {
					skpath, _ := p.Cloud().Domain().Validation().Keys()
					return returnString(skpath), nil
				}

				if l := k.GetPublicKey(); l != nil && l.GetGet() {
					_, pkpath := p.Cloud().Domain().Validation().Keys()
					return returnString(pkpath), nil
				}
			}

			if k := z.GetData(); k != nil {
				if l := k.GetPrivateKey(); l != nil && l.GetGet() {
					skr, err := p.Cloud().Domain().Validation().OpenPrivateKey()
					if err != nil {
						return nil, fmt.Errorf("failed to open domain validation private key: %w", err)
					}
					defer skr.Close()

					skdata, err := io.ReadAll(skr)
					if err != nil {
						return nil, fmt.Errorf("failed to read domain validation private key: %w", err)
					}

					return returnBytes(skdata), nil
				}

				if l := k.GetPublicKey(); l != nil && l.GetGet() {
					pkr, err := p.Cloud().Domain().Validation().OpenPublicKey()
					if err != nil {
						return nil, fmt.Errorf("failed to open domain validation public key: %w", err)
					}
					defer pkr.Close()

					pkdata, err := io.ReadAll(pkr)
					if err != nil {
						return nil, fmt.Errorf("failed to read domain validation public key: %w", err)
					}

					return returnBytes(pkdata), nil
				}
			}
		}

		// set
		if x := a.GetRoot(); x != nil && (!x.GetGet() || x.GetSet() != "") {
			return nil, p.Cloud().Domain().SetRoot(x.GetSet())
		}

		if x := a.GetGenerated(); x != nil && (!x.GetGet() || x.GetSet() != "") {
			return nil, p.Cloud().Domain().SetGenerated(x.GetSet())
		}

		if x := a.GetValidation(); x != nil {
			if x.GetGenerate() {
				return nil, p.Cloud().Domain().Validation().Generate()
			}

			if z := x.GetKeys(); z != nil {
				if k := z.GetPath(); k != nil {
					if l := k.GetPrivateKey(); l != nil && (!l.GetGet() || l.GetSet() != "") {
						return nil, p.Cloud().Domain().Validation().SetPrivateKey(l.GetSet())
					}

					if l := k.GetPublicKey(); l != nil && (!l.GetGet() || l.GetSet() != "") {
						return nil, p.Cloud().Domain().Validation().SetPublicKey(l.GetSet())
					}
				}

				if k := z.GetData(); k != nil {
					if l := k.GetPrivateKey(); l != nil && (!l.GetGet() || l.GetSet() != nil) {
						kw, err := p.Cloud().Domain().Validation().CreatePrivateKey()
						if err != nil {
							return nil, fmt.Errorf("failed to set domain private key: %w", err)
						}
						defer kw.Close()

						_, err = io.Copy(kw, bytes.NewBuffer(l.GetSet()))
						if err != nil {
							return nil, fmt.Errorf("failed to write domain private key: %w", err)
						}

						return nil, nil
					}

					if l := k.GetPublicKey(); l != nil && (!l.GetGet() || l.GetSet() != nil) {
						kw, err := p.Cloud().Domain().Validation().CreatePublicKey()
						if err != nil {
							return nil, fmt.Errorf("failed to set domain public key: %w", err)
						}
						defer kw.Close()

						_, err = io.Copy(kw, bytes.NewBuffer(l.GetSet()))
						if err != nil {
							return nil, fmt.Errorf("failed to write domain public key: %w", err)
						}

						return nil, nil
					}
				}
			}
		}
	}

	if a := in.GetP2P(); a != nil {
		if x := a.GetBootstrap(); x != nil {
			// get
			if x.GetList() {
				return returnStringSlice(p.Cloud().P2P().Bootstrap().List()), nil
			}

			if z := x.GetSelect(); z != nil {
				shape := z.GetShape()
				if n := z.GetNodes(); n != nil {
					// get
					if n.GetList() {
						return returnStringSlice(p.Cloud().P2P().Bootstrap().Shape(shape).List()), nil
					}

					// set
					if l := n.GetSet(); l != nil {
						return nil, p.Cloud().P2P().Bootstrap().Shape(shape).Set(l.GetValue()...)
					}

					if l := n.GetAdd(); l != nil {
						return nil, p.Cloud().P2P().Bootstrap().Shape(shape).Append(l.GetValue()...)
					}

					if l := n.GetDelete(); l != nil {
						for _, li := range l.GetValue() {
							if err := p.Cloud().P2P().Bootstrap().Shape(shape).Delete(li); err != nil {
								return nil, err
							}
						}

						return nil, nil
					}

					if n.GetClear() {
						for _, sh := range p.Cloud().P2P().Bootstrap().List() {
							if err := p.Cloud().P2P().Bootstrap().Delete(sh); err != nil {
								return nil, err
							}
						}

						return nil, nil
					}
				}
			}

			// set
			// None. Maybe add clear() later
		}

		if x := a.GetSwarm(); x != nil {
			if k := x.GetKey(); k != nil {
				// get
				if l := k.GetPath(); l != nil && l.GetGet() {
					return returnString(p.Cloud().P2P().Swarm().Get()), nil
				}

				if l := k.GetData(); l != nil && l.GetGet() {
					kr, err := p.Cloud().P2P().Swarm().Open()
					if err != nil {
						return nil, fmt.Errorf("failed to open swarm key: %w", err)
					}
					defer kr.Close()

					kdata, err := io.ReadAll(kr)
					if err != nil {
						return nil, fmt.Errorf("failed to read swarm key: %w", err)
					}

					return returnBytes(kdata), nil
				}

				//set
				if l := k.GetPath(); l != nil && (!l.GetGet() || l.GetSet() != "") {
					return nil, p.Cloud().P2P().Swarm().Set(l.GetSet())
				}

				if l := k.GetData(); l != nil && (!l.GetGet() || l.GetSet() != nil) {
					kw, err := p.Cloud().P2P().Swarm().Create()
					if err != nil {
						return nil, fmt.Errorf("failed to set swarm key: %w", err)
					}
					defer kw.Close()

					_, err = io.Copy(kw, bytes.NewBuffer(l.GetSet()))
					if err != nil {
						return nil, fmt.Errorf("failed to write swarm key: %w", err)
					}

					return nil, nil
				}
			}

			// generate
			if x.GetGenerate() {
				return nil, p.Cloud().P2P().Swarm().Generate()
			}
		}
	}

	return nil, errors.New("invalid cloud operation")
}
