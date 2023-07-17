package flat

type Object struct {
	Root []string
	data interface{} // keep data alive till query is deleted
	Data Items
}

type Item struct {
	Path []string
	Data interface{}
}

type Items []Item
