package runtime

func roundedUpDivWithUpperLimit(val, chunkSize, limit uint64) uint64 {
	count := val / chunkSize
	if val%chunkSize != 0 {
		count++
	}
	if count > limit {
		count = limit
	}

	return count
}
