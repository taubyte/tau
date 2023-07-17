package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/odo/protocols/node/components/storage/common"
	pathUtil "github.com/taubyte/utils/path"

	"github.com/alecthomas/units"
	storageIface "github.com/taubyte/go-interfaces/services/substrate/storage"
	storageSpec "github.com/taubyte/go-specs/storage"
	readerUtils "go4.org/readerutil"
)

func (s *Store) Used(ctx context.Context) (used int, err error) {
	sizeKeys, err := s.KVDB.List(ctx, common.KvSize)
	if err != nil {
		return 0, fmt.Errorf("getting sizes list failed with %w", err)
	}

	for _, key := range sizeKeys {
		sizeByte, err := s.KVDB.Get(ctx, key)
		if err != nil {
			return 0, fmt.Errorf("getting Size for key %s failed with %w", key, err)
		}

		size, err := strconv.Atoi(string(sizeByte))
		if err != nil {
			return 0, fmt.Errorf("getting sizes list failed with %w", err)
		}

		used += size
	}

	return
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if len(prefix) == 0 {
		entries, err := s.KVDB.List(ctx, prefix)
		if err != nil {
			return nil, fmt.Errorf("listing with empty prefix failed wit: %s", err)
		}

		var newList []string

		for _, entry := range entries {
			if !strings.HasPrefix(entry, "/s/") {
				newList = append(newList, entry)
			}
		}

		return newList, nil
	}

	return s.KVDB.List(ctx, prefix)
}

func (s *Store) Capacity() (capacity int) {
	return int(s.context.Config.Size)
}

func (s *Store) put(ctx context.Context, name string, v []byte) error {
	err := s.KVDB.Put(ctx, name, v)
	if err != nil {
		return fmt.Errorf("putting data to database failed with: %w", err)
	}

	return nil
}

// TODO: Version/timestamp:: What happens when 2 nodes try to update version at same time?
func (s *Store) AddFile(ctx context.Context, r io.ReadSeeker, name string, replace bool) (version int, err error) {
	_size, ok := readerUtils.Size(r)
	if !ok {
		return 0, errors.New("reading file size failed")
	}
	size := int(_size)

	version = 1
	if s.context.Config.Versioning {
		version, err = s.getNewVersion(ctx, name, replace)
		if err != nil {
			return
		}
	}

	versionString := strconv.Itoa(version)
	oldSize := 0
	oldSizeByte, err := s.Get(ctx, path.Join(common.KvSize, name, versionString))
	if err == nil || len(oldSizeByte) != 0 {
		if !replace {
			err = errors.New("another file with same name exists")
			return
		}

		oldSize, err = strconv.Atoi(string(oldSizeByte))
		if err != nil {
			err = fmt.Errorf("file with same name previously added, but file could not be read with %w", err)
			return
		}
	}

	used, err := s.Used(ctx)
	if err != nil {
		err = fmt.Errorf("adding file failed getting with %w", err)
		return
	}

	availableSpace := s.Capacity() - oldSize - used
	if availableSpace < size {
		availableSpace = (size - availableSpace)
		err = fmt.Errorf("cannot add file, remove %s", units.MetricBytes(availableSpace))
		return
	}

	if _, err = r.Seek(0, io.SeekStart); err != nil {
		err = fmt.Errorf("seeking to start of file failed with: %s", err)
		return
	}

	cid, err := s.srv.Node().AddFile(r)
	if err != nil {
		err = fmt.Errorf("adding file to peer node failed with: %w", err)
		return
	}

	if err = s.put(ctx, path.Join(storageSpec.FilePath.String(), name, versionString), []byte(cid)); err != nil {
		err = fmt.Errorf("adding file cid to database failed with: %w", err)
		return
	}

	if err = s.put(ctx, path.Join(common.KvVersion, name), []byte(versionString)); err != nil {
		err = fmt.Errorf("adding file version to database failed with: %w", err)
		return
	}

	sizeString := strconv.Itoa(size)
	if err = s.put(ctx, path.Join(common.KvSize, name, versionString), []byte(sizeString)); err != nil {
		err = fmt.Errorf("adding file size to database failed with: %w", err)
		return
	}

	return version, nil
}

// version 0 for latest version, -1 for all
func (s *Store) DeleteFile(ctx context.Context, name string, version int) error {
	latestVersion, err := s.Get(ctx, path.Join(common.KvVersion, name))
	if err != nil {
		return fmt.Errorf("getting latest version while deleting file %s failed with: %v", name, err)
	}

	if version < -1 {
		return fmt.Errorf("version %d is not a valid version, must be greater than or equal to -1", version)
	}

	switch version {
	case -1:
		latestVersionInt, err := strconv.Atoi(string(latestVersion))
		if err != nil {
			return fmt.Errorf("converting latest version to int while deleting file %s failed with: %v", name, err)
		}

		for i := latestVersionInt; i > 0; i-- {
			if err = s.delete(ctx, name, strconv.Itoa(i)); err != nil {
				continue
			}
		}

		versions, err := s.ListVersions(ctx, name)
		if err == nil {
			return fmt.Errorf("failed to delete all versions of file %s, versions %v still exist", name, versions)
		}

		if err = s.Delete(ctx, path.Join(common.KvVersion, name)); err != nil {
			return fmt.Errorf("deleting latest version index of file %s failed with %w", name, err)
		}
	case 0:
		if err = s.delete(ctx, name, string(latestVersion)); err != nil {
			return fmt.Errorf("deleting latest versions of file %s failed with: %v", name, err)
		}

		if _, err := s.GetLatestVersion(ctx, name); err != nil {
			return fmt.Errorf("updating latest version of file %s failed with: %v", name, err)
		}
	default:
		if err = s.delete(ctx, name, strconv.Itoa(version)); err != nil {
			return fmt.Errorf("deleting version %d of file %s failed with: %w", version, name, err)
		}

		if _, err := s.GetLatestVersion(ctx, name); err != nil {
			return fmt.Errorf("updating latest version of file %s failed with: %v", name, err)
		}
	}

	return nil

}

func (s *Store) delete(ctx context.Context, name string, version string) error {
	if _, err := s.Get(ctx, path.Join(common.KvSize, name, version)); err != nil {
		return errors.New("cannot delete file:" + name + ", file size not found")
	}

	cid, err := s.Get(ctx, path.Join(storageSpec.FilePath.String(), name, version))
	if err != nil {
		return errors.New("cannot delete file:" + name + ", not found")
	}

	if err = s.srv.Node().DeleteFile(string(cid)); err != nil {
		return fmt.Errorf("failed to delete file: %s, with: %w", name, err)
	}

	if err = s.Delete(ctx, path.Join(storageSpec.FilePath.String(), name, version)); err != nil {
		return fmt.Errorf("failed to delete cid from key value database with %w", err)
	}

	if err = s.Delete(ctx, path.Join(common.KvSize, name, version)); err != nil {
		return fmt.Errorf("failed to delete file size from key value database with %w", err)

	}

	return nil
}

// version 0 for latest storage
func (s *Store) Meta(ctx context.Context, name string, version int) (storageIface.Meta, error) {
	var versionString string
	if version == 0 {
		versionByte, err := s.Get(ctx, path.Join(common.KvVersion, name))
		if err != nil {
			return nil, fmt.Errorf("failed getting version bytes in meta with %v", err)
		}

		versionString = string(versionByte)
	} else {
		versionString = strconv.Itoa(version)
	}

	_cid, err := s.Get(ctx, path.Join(storageSpec.FilePath.String(), name, versionString))
	if err != nil {
		return nil, fmt.Errorf("failed getting meta for file %s with %v", name, err)
	}

	cid, err := cid.Decode(string(_cid))
	if err != nil {
		return nil, fmt.Errorf("decoding cid for file `%s` failed with: %v", name, err)
	}

	return &Meta{
		node:    s.srv.Node(),
		cid:     cid,
		version: version,
	}, nil
}

func (s *Store) Id() string {
	return s.id
}

func (s *Store) getNewVersion(ctx context.Context, name string, replace bool) (int, error) {
	versionData, err := s.Get(ctx, path.Join(common.KvVersion, name))
	if err != nil {
		return 1, nil
	}

	version, err := strconv.Atoi(string(versionData))
	if err != nil {
		return 0, err
	}

	if !replace {
		version++
	}

	return version, nil
}

func (s *Store) ListVersions(ctx context.Context, name string) ([]string, error) {
	paths, err := s.KVDB.List(ctx, path.Join(storageSpec.FilePath.String(), name))
	if err != nil {
		return nil, fmt.Errorf("listing versions failed with: %s", err)
	}

	var versions []string
	for _, _path := range paths {
		_split := pathUtil.Split(_path)
		splitLength := len(_split)
		if splitLength == 0 {
			return nil, fmt.Errorf("path %s is not in correct format", _path)
		}
		versions = append(versions, _split[splitLength-1])
	}

	if len(versions) == 0 {
		return nil, fmt.Errorf("no available versions for file %s", name)
	}

	return versions, nil
}

func (s *Store) GetLatestVersion(ctx context.Context, name string) (int, error) {
	versions, err := s.ListVersions(ctx, name)
	if err != nil {
		return 0, fmt.Errorf("updating latest version failed with: %w", err)
	}

	var latest int
	for _, version := range versions {
		versionInt, err := strconv.Atoi(version)
		if err != nil {
			return 0, fmt.Errorf("latest version setting to int failed with: %w", err)
		}

		if versionInt > latest {
			latest = versionInt
		}
	}

	err = s.put(ctx, path.Join(common.KvVersion, name), []byte(strconv.Itoa(latest)))
	if err != nil {
		return 0, fmt.Errorf("setting latest version failed with %w", err)
	}

	return latest, nil
}

func (s *Store) UpdateCapacity(size uint64) {
	s.context.Config.Size = size
}

func (s *Store) Close() {
	if s.KVDB != nil {
		s.KVDB.Close()
	}

	s.instanceCtxC()
}
