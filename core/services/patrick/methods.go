package patrick

func (j *Job) SetLog(key, value string) {
	j.LogLock.Lock()
	j.Logs[key] = value
	j.LogLock.Unlock()
}

func (j *Job) SetCid(key, value string) {
	j.CidLock.Lock()
	j.AssetCid[key] = value
	j.CidLock.Unlock()
}
