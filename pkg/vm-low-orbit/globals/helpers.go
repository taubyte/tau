package globals

import "path"

func (f *Factory) getPath(application, function uint32, name string) string {
	return path.Join(f.getPathPrefix(application, function), name)
}

func (f *Factory) getPathPrefix(application, function uint32) string {
	if application == 1 && function == 1 {
		return "/" + path.Join(f.parent.Context().Application(), f.parent.Context().Resource())
	}
	if application == 1 {
		return "/" + path.Join(f.parent.Context().Application())
	}
	if function == 1 {
		return "/" + path.Join(f.parent.Context().Resource())
	}

	return "/"
}
