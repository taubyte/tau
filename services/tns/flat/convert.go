package flat

func (f *Object) Interface() interface{} {
	switch len(f.Data) {
	case 0:
		return nil
	case 1:
		return f.Data[0].Data
	default:
		return f.toInterface()
	}
}

func (f *Object) toInterface() interface{} {
	object := make(map[string]interface{})
	for _, item := range f.Data {
		cur := object
		for idx, sub := range item.Path {
			next, ok := cur[sub]
			if ok {
				switch v := next.(type) {
				case map[string]interface{}:
					cur = v
				}
			} else {
				if idx == len(item.Path)-1 {
					cur[sub] = item.Data
				} else {
					cur[sub] = make(map[string]interface{})
					cur = cur[sub].(map[string]interface{})
				}
			}
		}
	}
	return object
}
