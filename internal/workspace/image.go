package workspace

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
)

// daemonDir is where the eikad binary is placed inside a container. The
// container's entrypoint is the binary at this path.
const daemonDir = "/usr/local/bin"

// createContainer creates the container, pulling the image once if the daemon
// does not have it yet.
func (h *Host) createContainer(ctx context.Context, cfg *container.Config, hostCfg *container.HostConfig, netCfg *network.NetworkingConfig, name string) (container.CreateResponse, error) {
	created, err := h.docker.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, name)
	if err == nil || !cerrdefs.IsNotFound(err) {
		return created, err
	}
	if err := h.pullImage(ctx, cfg.Image); err != nil {
		return container.CreateResponse{}, err
	}
	return h.docker.ContainerCreate(ctx, cfg, hostCfg, netCfg, nil, name)
}

// pullImage fetches an image the Docker daemon does not have.
func (h *Host) pullImage(ctx context.Context, ref string) error {
	body, err := h.docker.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", ref, err)
	}
	defer body.Close()
	if err := drainDockerStream(body); err != nil {
		return fmt.Errorf("pull image %s: %w", ref, err)
	}
	h.log.Info("sandbox image pulled", "image", ref)
	return nil
}

// buildImage builds a workspace image from the spec's build context and tags
// it as ref.
func (h *Host) buildImage(ctx context.Context, spec Spec, ref string) error {
	buildContext, err := tarDir(spec.BuildContext)
	if err != nil {
		return err
	}
	dockerfile := spec.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	resp, err := h.docker.ImageBuild(ctx, buildContext, build.ImageBuildOptions{
		Tags:       []string{ref},
		Dockerfile: dockerfile,
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("build image %s: %w", ref, err)
	}
	defer resp.Body.Close()
	if err := drainDockerStream(resp.Body); err != nil {
		return fmt.Errorf("build image %s: %w", ref, err)
	}
	h.log.Info("workspace image built", "image", ref, "context", spec.BuildContext)
	return nil
}

// injectDaemon copies the harness's eikad binary into a created container, so
// that any image works as a sandbox without being rebuilt.
//
// The binary is copied rather than bind-mounted because a bind mount source is
// resolved by the Docker daemon on the host, and the harness's own filesystem
// is inside a container the host cannot see.
func (h *Host) injectDaemon(ctx context.Context, containerID string) error {
	binary, err := os.ReadFile(h.opts.EikadBinary)
	if err != nil {
		return fmt.Errorf("read eikad binary %s: %w", h.opts.EikadBinary, err)
	}
	archive, err := tarFile("eikad", binary, 0o755)
	if err != nil {
		return err
	}
	if err := h.docker.CopyToContainer(ctx, containerID, daemonDir, archive, container.CopyToContainerOptions{}); err != nil {
		return fmt.Errorf("copy eikad into container: %w", err)
	}
	return nil
}

// tarFile wraps one file's contents in a tar archive.
func tarFile(name string, data []byte, mode int64) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: mode,
		Size: int64(len(data)),
	}); err != nil {
		return nil, fmt.Errorf("write tar header for %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return nil, fmt.Errorf("write tar entry for %s: %w", name, err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar archive: %w", err)
	}
	return &buf, nil
}

// tarDir packs a build context directory into a tar archive. Only directories
// and regular files are packed; a build context needs nothing else.
func tarDir(dir string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." {
			return err
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if entry.IsDir() {
			header.Name += "/"
			return tw.WriteHeader(header)
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pack build context %s: %w", dir, err)
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close build context archive: %w", err)
	}
	return &buf, nil
}

// drainDockerStream reads a Docker progress stream to the end and returns the
// error it reports, if any. The stream must be consumed fully or the daemon
// abandons the operation.
func drainDockerStream(r io.Reader) error {
	dec := json.NewDecoder(r)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read docker stream: %w", err)
		}
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
	}
}
