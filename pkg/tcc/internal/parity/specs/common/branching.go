package common

func Current(projectId, branch string) *TnsPath {
	return NewTnsPath(([]string{ProjectPathVariable.String(), projectId, BranchPathVariable.String(), branch, CurrentCommitPathVariable.String()}))
}

func (_path *TnsPath) Versioning() *VersioningPath {
	return &VersioningPath{_path}
}

func (b *VersioningPath) Commit(commit string) *TnsPath {
	return NewTnsPath(append([]string{commit}, b.Slice()...))
}

func (b *VersioningPath) Links() *TnsPath {
	return NewTnsPath(append(b.Slice(), LinksPathVariable.String()))
}
