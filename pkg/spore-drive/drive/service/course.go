package service

import (
	"context"
	"errors"

	"github.com/taubyte/tau/pkg/spore-drive/course"
	"github.com/taubyte/tau/pkg/spore-drive/drive"
	"github.com/taubyte/utils/id"
)

func (s *Service) newCourse(d *driveInstance, opts []course.Option) (ci *courseInstance, err error) {
	ci = &courseInstance{
		id:      id.Generate(d.id),
		drive:   d,
		service: s,
	}

	ci.ctx, ci.ctxC = context.WithCancel(s.ctx)

	ci.Course, err = course.New(d.Network(), opts...)
	if err != nil {
		return nil, errors.New("drive not found")
	}

	s.courseLock.Lock()
	s.courses[ci.id] = ci
	s.courseLock.Unlock()

	return ci, nil
}

func (s *Service) freeCourse(cId string) {
	s.courseLock.Lock()
	defer s.courseLock.Unlock()
	delete(s.courses, cId)
}

func (s *Service) getCourse(cId string) *courseInstance {
	s.courseLock.RLock()
	defer s.courseLock.RUnlock()
	return s.courses[cId]
}

func (ci *courseInstance) displace() error {
	ci.lock.Lock()
	defer ci.lock.Unlock()

	if ci.pch != nil {
		return errors.New("displacement in progress")
	}

	if ci.mockDisplace != nil {
		ci.pch = ci.mockDisplace()
	} else {
		ci.pch = ci.drive.Displace(ci.ctx, ci)
	}

	return nil
}

func (ci *courseInstance) progress() (<-chan drive.Progress, error) {
	ci.lock.Lock()
	defer ci.lock.Unlock()

	if ci.pch == nil {
		return nil, errors.New("no displacement")
	}

	return ci.pch, nil
}

func (ci *courseInstance) abort() {
	ci.ctxC()
	ci.service.freeCourse(ci.id)
}
