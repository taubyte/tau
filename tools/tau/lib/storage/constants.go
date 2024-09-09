package storageLib

const (
	BucketStreaming   = "Streaming"
	BucketObject      = "Object"
	LCBucketStreaming = "streaming"
	LCBucketObject    = "object"

	DefaultBucket = BucketObject
)

var (
	Buckets = []string{BucketObject, BucketStreaming}
)
