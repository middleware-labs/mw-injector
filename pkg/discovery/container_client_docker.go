package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

const dockerSocket = "/var/run/docker.sock"

type dockerClient struct {
	httpClient *http.Client
}

func newDockerClient() *dockerClient {
	return &dockerClient{
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.DialTimeout("unix", dockerSocket, 2*time.Second)
				},
			},
			Timeout: 10 * time.Second,
		},
	}
}

func (d *dockerClient) Name() string { return "docker" }

func (d *dockerClient) Available(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/_ping", nil)
	if err != nil {
		return false
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// dockerInspectResponse is a minimal subset of Docker's container inspect JSON.
type dockerInspectResponse struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

func (d *dockerClient) InspectBatch(ctx context.Context, ids []string) (map[string]ContainerMeta, error) {
	result := make(map[string]ContainerMeta, len(ids))
	for _, id := range ids {
		meta, err := d.inspect(ctx, id)
		if err != nil {
			continue
		}
		result[id] = meta
	}
	return result, nil
}

func (d *dockerClient) inspect(ctx context.Context, id string) (ContainerMeta, error) {
	url := fmt.Sprintf("http://docker/containers/%s/json", id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ContainerMeta{}, err
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return ContainerMeta{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ContainerMeta{}, fmt.Errorf("docker inspect %s: status %d", id, resp.StatusCode)
	}

	var raw dockerInspectResponse
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

func splitImageTag(ref string) (string, string) {
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i:], "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

func extractComposeInfo(labels map[string]string) *ComposeInfo {
	project := labels["com.docker.compose.project"]
	service := labels["com.docker.compose.service"]
	if project == "" && service == "" {
		return nil
	}
	return &ComposeInfo{
		Project: project,
		Service: service,
		WorkDir: labels["com.docker.compose.project.working_dir"],
	}
}
