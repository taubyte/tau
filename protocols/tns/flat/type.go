package flat

type Object struct {
	Root []string
	data interface{}
	Data Items
}

type Item struct {
	Path []string
	Data interface{}
}

type Items []Item
