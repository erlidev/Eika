package computer

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	cerrdefs "github.com/containerd/errdefs"
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

// HasImage reports whether the Docker daemon holds the image ref. An error
// means the daemon could not be asked at all, which is how a socket the
// harness cannot reach, or may not use, shows itself.
func (h *Host) HasImage(ctx context.Context, ref string) (bool, error) {
	_, err := h.docker.ImageInspect(ctx, ref)
	if err == nil {
		return true, nil
	}
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("inspect image %s: %w", ref, err)
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

// ownRoot makes the user the container runs as the owner of the computer
// root. A volume starts out as the image's directory at the root, owned as
// the image owns it; an image with no such directory leaves the volume
// owned by root, which the user could read and never write. The container
// has not started, so the daemon extracts the directory entry itself,
// carrying the owner in the archive.
func (h *Host) ownRoot(ctx context.Context, containerID string) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeDir,
		Name:     path.Base(Root) + "/",
		Mode:     0o755,
		Uid:      userID,
		Gid:      groupID,
	}); err != nil {
		return fmt.Errorf("write tar header for %s: %w", Root, err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar archive: %w", err)
	}
	if err := h.docker.CopyToContainer(ctx, containerID, path.Dir(Root), &buf, container.CopyToContainerOptions{
		CopyUIDGID: true,
	}); err != nil {
		return fmt.Errorf("own %s in container: %w", Root, err)
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
