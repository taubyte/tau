package service

import (
	"errors"
	"fmt"

	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
)

type hasGetNode interface {
	GetNode() *pb.Node
}

func (s *Service) getNode(req hasGetNode) (*instance, error) {
	var nid string
	if x := req.GetNode(); x != nil {
		nid = x.GetId()
	}

	return s.getNodeById(nid)
}

func (s *Service) getNodeById(nid string) (*instance, error) {
	if nid == "" {
		return nil, errors.New("empty node id")
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	ni, exist := s.nodes[nid]
	if !exist {
		return nil, fmt.Errorf("node `%s` does not exist", nid)
	}

	return ni, nil
}
