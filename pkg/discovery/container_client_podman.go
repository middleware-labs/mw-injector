package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type podmanClient struct {
	socketPath string
	httpClient *http.Client
}

func newPodmanClient() *podmanClient {
	sock := findPodmanSocket()
	return &podmanClient{
		socketPath: sock,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.DialTimeout("unix", sock, 2*time.Second)
				},
			},
			Timeout: 10 * time.Second,
		},
	}
}

func findPodmanSocket() string {
	if sock := os.Getenv("CONTAINER_HOST"); sock != "" {
		return strings.TrimPrefix(sock, "unix://")
	}
	// Root socket
	if _, err := os.Stat("/run/podman/podman.sock"); err == nil {
		return "/run/podman/podman.sock"
	}
	// Rootless socket
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		sock := filepath.Join(xdg, "podman", "podman.sock")
		if _, err := os.Stat(sock); err == nil {
			return sock
		}
	}
	return "/run/podman/podman.sock"
}

func (p *podmanClient) Name() string { return "podman" }

func (p *podmanClient) Available(ctx context.Context) bool {
	if _, err := os.Stat(p.socketPath); err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://podman/_ping", nil)
	if err != nil {
		return false
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// podmanInspectResponse mirrors the Docker-compat inspect JSON that Podman serves.
type podmanInspectResponse struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

func (p *podmanClient) InspectBatch(ctx context.Context, ids []string) (map[string]ContainerMeta, error) {
	result := make(map[string]ContainerMeta, len(ids))
	for _, id := range ids {
		meta, err := p.inspect(ctx, id)
		if err != nil {
			continue
		}
		result[id] = meta
	}
	return result, nil
}

func (p *podmanClient) inspect(ctx context.Context, id string) (ContainerMeta, error) {
	url := fmt.Sprintf("http://podman/containers/%s/json", id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ContainerMeta{}, err
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return ContainerMeta{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ContainerMeta{}, fmt.Errorf("podman inspect %s: status %d", id, resp.StatusCode)
	}

	var raw podmanInspectResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return ContainerMeta{}, err
	}

	name := strings.TrimPrefix(raw.Name, "/")
	image, tag := splitImageTag(raw.Config.Image)

	meta := ContainerMeta{
		ID:     raw.ID,
		Name:   name,
		Image:  image,
		Labels: raw.Config.Labels,
	}
	if tag != "" {
		meta.ImageTag = tag
	}
	if ci := extractComposeInfo(raw.Config.Labels); ci != nil {
		meta.ComposeInfo = ci
	}
	return meta, nil
}
