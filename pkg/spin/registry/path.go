package registry

import "os"

func (r *registry) Path(image string) (string, error) {
	if dgt, hit := r.cacheGet(image); hit {
		return r.imageFilePath(dgt.Encoded()), nil
	}
	return "", os.ErrNotExist
}
