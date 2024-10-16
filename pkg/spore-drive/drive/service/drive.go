package service

import (
	"fmt"

	"github.com/taubyte/tau/pkg/spore-drive/drive"
	"github.com/taubyte/utils/id"
)

func (s *Service) newDrive(configId string, opts []drive.Option) (*driveInstance, error) {
	cnf, err := s.resolver.Lookup(configId)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup config by id with %w", err)
	}

	sd, err := drive.New(cnf, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create drive with %w", err)
	}

	d := &driveInstance{
		id:    id.Generate(fmt.Sprintf("%p", cnf)),
		Spore: sd,
	}

	s.driveLock.Lock()
	s.drives[d.id] = d
	s.driveLock.Unlock()

	return d, nil
}

func (s *Service) freeDrive(driveId string) {
	s.driveLock.Lock()
	defer s.driveLock.Unlock()
	delete(s.drives, driveId)
}

func (s *Service) getDrive(driveId string) *driveInstance {
	s.driveLock.RLock()
	defer s.driveLock.RUnlock()
	return s.drives[driveId]
}
