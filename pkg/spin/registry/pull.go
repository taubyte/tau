package registry

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	oras "oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/containerd/containerd/archive"
	"github.com/taubyte/tau/pkg/spin/embed"
	"github.com/taubyte/tau/pkg/spin/runtime"

	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/images"
	"github.com/opencontainers/go-digest"
	spec "github.com/opencontainers/image-spec/specs-go/v1"

	ctdoci "github.com/containerd/containerd/oci"

	ctdnamespaces "github.com/containerd/containerd/namespaces"

	runtimespec "github.com/opencontainers/runtime-spec/specs-go"

	ctdcontainers "github.com/containerd/containerd/containers"

	"github.com/CalebQ42/squashfs"

	//lint:ignore ST1001 ignore
	. "github.com/taubyte/tau/pkg/spin"
)

type pullProgress struct {
	err        error
	completion int
}

func (p pullProgress) Error() error {
	return p.err
}

func (p pullProgress) Completion() int {
	return p.completion
}

func pushProgress(progress chan<- PullProgress, prog pullProgress) {
	if progress != nil {
		select {
		case progress <- prog:
		default:
		}
	}
}

func (r *registry) Pull(ctx context.Context, image string, progress chan<- PullProgress) error {
	pCtx, pCtxC := context.WithCancel(r.ctx)
	defer pCtxC()

	pushProgress(progress, pullProgress{})
	defer pushProgress(progress, pullProgress{err: io.EOF})

	go func() {
		select {
		case <-ctx.Done():
			pCtxC()
		case <-pCtx.Done():
			return
		}
	}()

	retChan := make(chan error, 1)

	r.pullRequest <- &pullRequest{
		ctx:        pCtx,
		image:      image,
		registries: r.registries,
		ret:        retChan,
		progress:   progress,
	}

	select {
	case <-pCtx.Done():
		return pCtx.Err()
	case ret := <-retChan:
		return ret
	}

}

func (r *registry) imageFilePath(imageDigest string) string {
	return path.Join(r.root, "images", imageDigest)
}

func (r *registry) cacheGet(image string) (digest.Digest, bool) {
	r.lock.RLock()
	defer r.lock.RUnlock()

	item := r.imageToDigestCache.Get(image)

	if item != nil {
		return item.Value(), !item.IsExpired()
	}

	return digest.Digest(""), false
}

func (r *registry) hasImageOf(imageDigest string) bool {
	fi, err := os.Stat(r.imageFilePath(imageDigest))
	return err == nil && !fi.IsDir()
}

func (r *registry) addToCach(image string, imageDigest digest.Digest) {
	r.imageToDigestCache.Set(image, imageDigest, DigestResolvCacheTTL)
}

func (r *registry) pullRequestHandler() {
	imagesChan := make(map[string][]chan error)
	type pullRet struct {
		image string
		err   error
	}
	runningPull := make(chan struct{}, 16)
	donePull := make(chan pullRet, 16)

	// clean shutdown
	defer func() {
		close(runningPull)
		for range runningPull {
		}
		close(donePull)
		for range donePull {
		}
		r.wg.Done()
	}()

	for {
		select {
		case <-r.ctx.Done():
			return
		case reqChan := <-r.pullRequest:
			pushProgress(reqChan.progress, pullProgress{})
			if _, hit := r.cacheGet(reqChan.image); hit {
				pushProgress(reqChan.progress, pullProgress{completion: 100})
				continue
			}
			pushProgress(reqChan.progress, pullProgress{completion: 1, err: errors.New("not cached")})

			imagesChan[reqChan.image] = append(imagesChan[reqChan.image], reqChan.ret)
			if len(imagesChan[reqChan.image]) == 1 { // we're first, start pulling
				go func() {
					pushProgress(reqChan.progress, pullProgress{completion: 2})
					select {
					case <-reqChan.ctx.Done():
						pushProgress(reqChan.progress, pullProgress{completion: 100, err: reqChan.ctx.Err()})
						return
					case runningPull <- struct{}{}:
					}

					var err error
					defer func() {
						donePull <- pullRet{image: reqChan.image, err: err}
						pushProgress(reqChan.progress, pullProgress{completion: 100, err: err})
					}()

					pushProgress(reqChan.progress, pullProgress{completion: 3, err: err})
					var (
						imageRef string
						repo     *remote.Repository
						repoDesc spec.Descriptor
					)
					for _, registry := range r.registries {
						imageRef = fmt.Sprintf("%s/%s", registry, reqChan.image)
						if repo, err = remote.NewRepository(imageRef); err != nil {
							continue
						}

						if repoDesc, err = repo.Resolve(context.Background(), repo.Reference.Reference); err != nil {
							repo = nil
						} else {
							break
						}
					}

					if repo == nil {
						err = fmt.Errorf("failed to fetch image %s", reqChan.image)
						return
					}

					pushProgress(reqChan.progress, pullProgress{completion: 10, err: err})

					imageDigest := repoDesc.Digest.Encoded()

					if !r.hasImageOf(imageDigest) {
						var workPath string
						workPath, err = os.MkdirTemp("", "spin")
						if err != nil {
							err = fmt.Errorf("creating temporary pull folder failed with %w", err)
							return
						}
						defer os.RemoveAll(workPath)

						if err = r.pullFromRegistry(reqChan.ctx, repo, imageRef, workPath, reqChan.progress); err != nil {
							return
						}

						if err != nil {
							err = fmt.Errorf("pulling image %s failed with %w", reqChan.image, err)
							return
						}

						err = convImage(reqChan.ctx, workPath, r.imageFilePath(imageDigest), reqChan.progress)
					}

					pushProgress(reqChan.progress, pullProgress{completion: 99, err: err})

					r.addToCach(reqChan.image, repoDesc.Digest)

					select {
					case <-runningPull:
					default:
					}

				}()
			}
		case pRet := <-donePull:
			if pRet.err == nil {
				for _, rc := range imagesChan[pRet.image] {
					rc <- pRet.err
					close(rc)
				}
				delete(imagesChan, pRet.image)
			} else { // let other requests try
				rc := imagesChan[pRet.image][0]
				imagesChan[pRet.image] = imagesChan[pRet.image][1:]
				rc <- pRet.err
				close(rc)
			}

		}
	}
}

func convImage(ctx context.Context, workPath, outputFilename string, progress chan<- PullProgress) (err error) {
	rootfs := path.Join(workPath, "rootfs")

	if err := os.Mkdir(rootfs, 0755); err != nil {
		return fmt.Errorf("failed to create rootfs directory at '%s': %w", rootfs, err)
	}

	idxR, err := os.Open(filepath.Join(workPath, "index.json"))
	if err != nil {
		return fmt.Errorf("failed to open 'index.json' in '%s': %w", workPath, err)
	}
	defer idxR.Close()

	var idx spec.Index
	if err := json.NewDecoder(idxR).Decode(&idx); err != nil {
		return fmt.Errorf("failed to decode 'index.json': %w", err)
	}

	if len(idx.Manifests) == 0 {
		return errors.New("no manifests found in 'index.json'")
	}

	var manifestDigest *digest.Digest
	for _, manifest := range idx.Manifests {
		if manifest.Platform == nil {
			continue
		}
		arch := manifest.Platform.Architecture
		if arch == "amd64" || arch == "riscv64" {
			manifestDigest = &manifest.Digest
		}
	}

	if manifestDigest == nil {
		manifestDigest = &idx.Manifests[0].Digest
	}

	manifestFile, err := os.Open(workPath + "/blobs/sha256/" + manifestDigest.Encoded())
	if err != nil {
		return fmt.Errorf("failed to open manifest file for digest '%s': %w", manifestDigest.Encoded(), err)
	}
	defer manifestFile.Close()

	var manifest spec.Manifest
	if err := json.NewDecoder(manifestFile).Decode(&manifest); err != nil {
		return fmt.Errorf("failed to decode manifest file: %w", err)
	}

	configFile, err := os.Open(workPath + "/blobs/sha256/" + manifest.Config.Digest.Encoded())
	if err != nil {
		return fmt.Errorf("failed to open config file for digest '%s': %w", manifest.Config.Digest.Encoded(), err)
	}
	defer configFile.Close()

	var image spec.Image
	imageD, err := io.ReadAll(configFile)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := json.NewDecoder(bytes.NewReader(imageD)).Decode(&image); err != nil {
		return fmt.Errorf("failed to decode image configuration: %w", err)
	}

	if err := os.Mkdir(path.Join(workPath, "config"), 0755); err != nil {
		return fmt.Errorf("failed to create configuration directory at '%s': %w", path.Join(workPath, "config"), err)
	}

	if err := os.WriteFile(path.Join(workPath, "config/imageconfig.json"), imageD, 0640); err != nil {
		return fmt.Errorf("failed to write image configuration to '%s': %w", path.Join(workPath, "config/imageconfig.json"), err)
	}

	cnf, err := generateSpec(ctx, &image)
	if err != nil {
		return fmt.Errorf("failed to generate runtime spec from image configuration: %w", err)
	}

	specD, err := json.Marshal(cnf)
	if err != nil {
		return fmt.Errorf("failed to marshal runtime spec: %w", err)
	}

	if err := os.WriteFile(path.Join(workPath, "config/config.json"), specD, 0640); err != nil {
		return fmt.Errorf("failed to write runtime spec to '%s': %w", path.Join(workPath, "config/config.json"), err)
	}

	pushProgress(progress, pullProgress{completion: 55})

	if _, err = unpackOCI(ctx, workPath, rootfs, idx.Manifests); err != nil {
		return fmt.Errorf("failed to unpack OCI image to '%s': %w", rootfs, err)
	}

	pushProgress(progress, pullProgress{completion: 65})

	if err = tarIt(ctx, rootfs, rootfs+".tar"); err != nil {
		return fmt.Errorf("failed to create tarball of rootfs at '%s': %w", rootfs+".tar", err)
	}

	pushProgress(progress, pullProgress{completion: 70})

	squashSrc, err := embed.ToolsSquashFS()
	if err != nil {
		return fmt.Errorf("failed to load squashFS tools: %w", err)
	}

	s, err := runtime.New(ctx, runtime.Module(squashSrc))
	if err != nil {
		return fmt.Errorf("failed to initialize squashFS spin: %w", err)
	}

	pushProgress(progress, pullProgress{completion: 73})

	sqfstar, err := s.New(runtime.Mount(workPath, "/mnt"), runtime.Command("/bin/sh", "-c", "/bin/sqfstar -quiet -no-progress -Xcompression-level 1 -Xstrategy fixed -mem 512M /mnt/rootfs.bin < /mnt/rootfs.tar"))
	if err != nil {
		return fmt.Errorf("failed to create squashFS container: %w", err)
	}

	if err = sqfstar.Run(); err != nil {
		return fmt.Errorf("failed to execute sqfstar command: %w", err)
	}

	pushProgress(progress, pullProgress{completion: 90})

	sqf, err := os.Open(rootfs + ".bin")
	if err != nil {
		return errors.New("can't find squashed rootfs")
	}

	sq, err := squashfs.NewReader(sqf)
	if err != nil {
		return fmt.Errorf("open squashed rootfs failed with %w", err)
	}

	if sq.Low.Superblock.Magic != uint32(0x73717368) {
		return errors.New("bad magic number")
	}

	if sq.Low.Superblock.CompType != uint16(1) {
		return errors.New("bad compression type")
	}

	defer pushProgress(progress, pullProgress{completion: 98})

	return zipIt(ctx, workPath, outputFilename, "/rootfs.bin", "/index.json", "/config/config.json", "/config/imageconfig.json")
}

func (r *registry) pullFromRegistry(ctx context.Context, repo *remote.Repository, imageRef, dstPath string, progress chan<- PullProgress) error {

	store, err := oci.New(dstPath)
	if err != nil {
		return err
	}

	// TODO: add a go routine that estimates the progress
	defer pushProgress(progress, pullProgress{completion: 50, err: err})

	_, err = oras.Copy(ctx, repo, imageRef, store, imageRef, oras.DefaultCopyOptions)
	if err != nil {
		return fmt.Errorf("failed to pull image from registry: %w", err)
	}

	return nil
}

func generateSpec(ctx context.Context, config *spec.Image) (_ *runtimespec.Spec, err error) {
	ic := config.Config
	ctdCtx := ctdnamespaces.WithNamespace(ctx, "default")
	s, err := ctdoci.GenerateSpecWithPlatform(ctdCtx, nil, "linux/"+config.Architecture, &ctdcontainers.Container{},
		ctdoci.WithHostNamespace(runtimespec.NetworkNamespace),
		ctdoci.WithoutRunMount,
		ctdoci.WithEnv(ic.Env),
		ctdoci.WithTTY, // TODO: make it configurable?
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate spec: %w", err)
	}

	args := ic.Entrypoint
	if len(ic.Cmd) > 0 {
		args = append(args, ic.Cmd...)
	}
	if len(args) > 0 {
		s.Process.Args = args
	}
	if ic.WorkingDir != "" {
		s.Process.Cwd = ic.WorkingDir
	}

	s.Linux.Seccomp = nil
	s.Root = &runtimespec.Root{
		Path: "/run/rootfs",
	}

	return s, nil
}

func isContainerManifest(manifest spec.Manifest) bool {
	if !images.IsConfigType(manifest.Config.MediaType) {
		return false
	}
	for _, desc := range manifest.Layers {
		if !images.IsLayerType(desc.MediaType) {
			return false
		}
	}
	return true
}

func unpackOCI(ctx context.Context, imgDir string, rootfs string, descs []spec.Descriptor) (io.Reader, error) {
	var children []spec.Descriptor
	for _, desc := range descs {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			switch desc.MediaType {
			case spec.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
				mfstD, err := os.ReadFile(filepath.Join(imgDir, "/blobs/sha256", desc.Digest.Encoded()))
				if err != nil {
					return nil, fmt.Errorf("opening manifest failed with %w", err)
				}

				var manifest spec.Manifest
				if err := json.Unmarshal(mfstD, &manifest); err != nil {
					return nil, fmt.Errorf("parsing manifest failed with %w", err)
				}

				if !isContainerManifest(manifest) {
					continue
				}

				configD, err := os.ReadFile(filepath.Join(imgDir, "/blobs/sha256", manifest.Config.Digest.Encoded()))
				if err != nil {
					return nil, fmt.Errorf("opening manifest config failed with %w", err)
				}

				for _, layerDesc := range manifest.Layers {
					if err := func() error {
						layerR, err := os.Open(filepath.Join(imgDir, "/blobs/sha256", layerDesc.Digest.Encoded()))
						if err != nil {
							return fmt.Errorf("opening layer failed with %w", err)
						}
						defer layerR.Close()

						r, err := compression.DecompressStream(layerR)
						if err != nil {
							return fmt.Errorf("decompress layer failed with %w", err)
						}

						if _, err := archive.Apply(ctx, rootfs, r, archive.WithNoSameOwner()); err != nil {
							return fmt.Errorf("apply layer failed with %w", err)
						}

						return nil
					}(); err != nil {
						return nil, err
					}
				}

				return bytes.NewReader(configD), nil
			case images.MediaTypeDockerSchema2ManifestList, spec.MediaTypeImageIndex:
				idxD, err := os.ReadFile(filepath.Join(imgDir, "/blobs/sha256", desc.Digest.Encoded()))
				if err != nil {
					return nil, fmt.Errorf("opening index failed with %w", err)
				}

				var idx spec.Index
				if err := json.Unmarshal(idxD, &idx); err != nil {
					return nil, fmt.Errorf("parsing index failed with %w", err)
				}

				children = append(children, idx.Manifests...)
			default:
				return nil, fmt.Errorf("unsupported mediatype %v", desc.MediaType)
			}
		}
		if len(children) > 0 {
			var childrenDescs []spec.Descriptor
			childrenDescs = append(childrenDescs, children...)
			sort.SliceStable(childrenDescs, func(i, j int) bool {
				if childrenDescs[i].Platform == nil {
					return false
				}
				if childrenDescs[j].Platform == nil {
					return true
				}
				return true
			})
			children = childrenDescs
		}
	}

	if len(children) > 0 {
		return unpackOCI(ctx, imgDir, rootfs, children)
	}

	return nil, fmt.Errorf("target config not found")
}

func tarIt(ctx context.Context, rootfsDir, outputTarball string) error {
	tarFile, err := os.Create(outputTarball)
	if err != nil {
		return fmt.Errorf("failed to create tarball file: %w", err)
	}
	defer tarFile.Close()

	tw := tar.NewWriter(tarFile)
	defer tw.Close()

	err = filepath.Walk(rootfsDir, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relFile, err := filepath.Rel(rootfsDir, file)
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:

			linkname, _ := os.Readlink(file)
			header, err := tar.FileInfoHeader(fi, filepath.Clean(linkname))
			if err != nil {
				return fmt.Errorf("failed to create tar header: %w", err)
			}

			header.Name = filepath.Clean(filepath.ToSlash("/" + relFile))

			if fname := path.Base(header.Name); fname == ".." || fname == "." {
				return nil
			}

			header.Uid, header.Gid = 0, 0
			header.Uname, header.Gname = "root", "root"

			if err := tw.WriteHeader(header); err != nil {
				return fmt.Errorf("failed to write tar header: %w", err)
			}

			if fi.Mode().IsRegular() {
				var fileContent *os.File
				fileContent, err = os.Open(file)
				if err != nil {
					return fmt.Errorf("failed to open file: %w", err)
				}
				defer fileContent.Close()

				if _, err := io.Copy(tw, fileContent); err != nil {
					return fmt.Errorf("failed to write file content to tarball: %w", err)
				}
			}

			return nil
		}
	})

	if err != nil {
		return fmt.Errorf("failed to walk rootfs directory: %w", err)
	}

	return nil
}

func zipIt(ctx context.Context, workDir, outputFilename string, files ...string) error {
	outFile, err := os.Create(outputFilename)
	if err != nil {
		return fmt.Errorf("failed to create zip file %s: %w", outputFilename, err)
	}
	defer outFile.Close()

	zipWriter := zip.NewWriter(outFile)
	defer zipWriter.Close()

	err = filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			relPath, err := filepath.Rel(workDir, path)
			if err != nil {
				return err
			}
			relPath = filepath.ToSlash("/" + relPath)

			if len(files) > 0 {
				include := false
				for _, file := range files {
					if file == relPath || strings.HasPrefix(file, relPath+"/") {
						include = true
						break
					}
				}
				if !include {
					return nil
				}
			}

			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return fmt.Errorf("failed to get file info for %s: %w", path, err)
			}

			header.Name = relPath
			if info.IsDir() {
				header.Name += "/"
			}

			writer, err := zipWriter.CreateHeader(header)
			if err != nil {
				return fmt.Errorf("failed to create zip header for %s: %w", relPath, err)
			}

			if !info.IsDir() {
				fileToZip, err := os.Open(path)
				if err != nil {
					return fmt.Errorf("failed to open file %s: %w", path, err)
				}
				defer fileToZip.Close()

				if _, err := io.Copy(writer, fileToZip); err != nil {
					return fmt.Errorf("failed to write file %s to zip: %w", relPath, err)
				}
			}

			return nil
		}
	})

	if err != nil {
		return fmt.Errorf("failed to walk the directory %s: %w", workDir, err)
	}

	return nil
}
