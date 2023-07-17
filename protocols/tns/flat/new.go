package flat

func New(path []string, new interface{}) (*Object, error) {
	_items, err := parseInterface(
		[]string{},
		new,
	)
	if err != nil {
		return nil, err
	}

	return &Object{
		Root: path,
		data: new,
		Data: _items,
	}, nil
}

func Empty(path []string) *Object {
	return &Object{
		Root: path,
		data: nil,
		Data: make(Items, 0),
	}
}
