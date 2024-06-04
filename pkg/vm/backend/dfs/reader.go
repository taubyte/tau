package dfs

import "fmt"

func (zw *zWasmReadCloser) Close() error {
	err := zw.unCompress.Close()
	if err != nil {
		return fmt.Errorf("closing uncompressed file failed with: %s", err)
	}
	err = zw.dag.Close()
	if err != nil {
		return fmt.Errorf("closing compressed file failed with: %s", err)
	}
	return nil
}

func (zw *zWasmReadCloser) Read(p []byte) (n int, err error) {
	return zw.unCompress.Read(p)
}

func (zip *zipReadCloser) Close() error {
	zip.ReadCloser.Close()
	return zip.parent.Close()
}
