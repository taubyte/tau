package drive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/pkg/errors"
)

func getLatestAssetVersion() (string, error) {
	var latest struct {
		Version string `json:"tag_name" cbor:"4,keyasint"`
	}

	req, err := http.NewRequest("GET", "https://api.github.com/repos/taubyte/tau/releases/latest", nil)
	if err != nil {
		return "", fmt.Errorf("cerating request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&latest)
	if err != nil {
		return "", fmt.Errorf("decode failed with: %s", err)
	}

	return normalizeVersion(latest.Version), nil
}

func normalizeVersion(version string) string {
	if version[0] == 'v' {
		return version[1:]
	}
	return version
}

func downloadTau(version, arch string) ([]byte, error) {
	version = normalizeVersion(version)

	if arch == "x86_64" {
		arch = "amd64"
	}

	asset := fmt.Sprintf("tau_%s_linux_%s.tar.gz", version, arch)
	defer os.Remove(asset)

	// Download
	downloadUrl := fmt.Sprintf("https://github.com/taubyte/tau/releases/download/v%s/%s", version, asset)
	tauBin, err := httpDownload(downloadUrl)
	if err != nil {
		return nil, fmt.Errorf("downloading failed with: %s", err)
	}

	return tauBin, nil
}

func httpDownload(downloadUrl string) ([]byte, error) {
	req, err := http.NewRequest("GET", downloadUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("new http request failed: %w", err)
	}

	req.Header.Set("User-Agent", "spore-drive")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client do failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected response status: %s", resp.Status)
	}

	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break // End of tar archive
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar header: %w", err)
		}

		if header.Name == "tau" {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, tarReader); err != nil {
				return nil, fmt.Errorf("failed to read 'tau' from tar: %w", err)
			}
			return buf.Bytes(), nil
		}
	}

	return nil, errors.New("file 'tau' not found in archive")
}
