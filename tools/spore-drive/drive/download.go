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

// releases is where tau's builds are published: the latest release's document,
// and the base of every asset's URL.
type releases struct {
	latest, download string
}

// published is tau's releases on GitHub.
var published = releases{
	latest:   "https://api.github.com/repos/taubyte/tau/releases/latest",
	download: "https://github.com/taubyte/tau/releases/download",
}

func (r releases) latestVersion() (string, error) {
	type latestRelease struct {
		Version string `json:"tag_name"`
	}
	var latest latestRelease

	req, err := http.NewRequest("GET", r.latest, nil)
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

func (r releases) tau(version, arch string) ([]byte, error) {
	version = normalizeVersion(version)

	if arch == "x86_64" {
		arch = "amd64"
	}

	asset := fmt.Sprintf("tau_%s_linux_%s.tar.gz", version, arch)
	defer os.Remove(asset)

	downloadUrl := fmt.Sprintf("%s/v%s/%s", r.download, version, asset)
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
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar header: %w", err)
		}

		if header.Name == "taud" {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, tarReader); err != nil {
				return nil, fmt.Errorf("failed to read 'taud' from tar: %w", err)
			}
			return buf.Bytes(), nil
		}
	}

	return nil, errors.New("file 'taud' not found in archive")
}
