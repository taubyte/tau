package http

func (u *Universe) KillService(service string) error {
	// Skipping error check here, as the api provides a good error
	return u.client.delete("/service/"+u.Name+"/"+service, nil, nil)
}

func (u *Universe) KillSimple(simple string) error {
	// Skipping error check here, as the api provides a good error
	return u.client.delete("/simple/"+u.Name+"/"+simple, nil, nil)
}

func (u *Universe) Kill() error {
	return u.client.delete("/universe/"+u.Name, nil, nil)
}
