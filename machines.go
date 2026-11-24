package jinn

import (
	"context"
	"net/url"
	"time"
)

// SSHKey is an account's authorised SSH public key.
type SSHKey struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"public_key"`
	CreatedAt   time.Time `json:"created_at"`
	CreatedBy   string    `json:"created_by"`
}

type SSHKeyRequest struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

func (c *Client) SSHKeys(ctx context.Context) ([]SSHKey, error) {
	var out struct {
		SSHKeys []SSHKey `json:"ssh_keys"`
	}
	return out.SSHKeys, c.do(ctx, "GET", "/v1/ssh-keys", nil, &out)
}

func (c *Client) AddSSHKey(ctx context.Context, req SSHKeyRequest) (SSHKey, error) {
	var k SSHKey
	return k, c.do(ctx, "POST", "/v1/ssh-keys", req, &k)
}

func (c *Client) DeleteSSHKey(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/ssh-keys/"+url.PathEscape(id), nil, nil)
}

// Machine is a persistent Linux VM backed by a lineage of Jinn images.
type Machine struct {
	ID                string     `json:"id"`
	Account           string     `json:"account"`
	Slug              string     `json:"slug"`
	Name              string     `json:"name,omitempty"`
	Size              string     `json:"size"`
	Image             string     `json:"image"`
	ImageDeleted      bool       `json:"image_deleted,omitempty"`
	Setup             []string   `json:"setup,omitempty"`
	Environment       []EnvVar   `json:"environment,omitempty"`
	Webhook           string     `json:"webhook,omitempty"`
	ExternalReference string     `json:"external_reference,omitempty"`
	TimeoutMinutes    int        `json:"timeout_minutes,omitempty"`
	IdleMinutes       int        `json:"idle_minutes"`
	State             string     `json:"state"`
	ActiveRun         string     `json:"active_run,omitempty"`
	IdleSince         *time.Time `json:"idle_since,omitempty"`
	SSH               string     `json:"ssh,omitempty"`
	ActiveConnections int        `json:"active_connections"`
	CreatedAt         time.Time  `json:"created_at"`
	CreatedBy         string     `json:"created_by"`
}

type MachineRequest struct {
	Name              string   `json:"name,omitempty"`
	Size              string   `json:"size,omitempty"`
	Image             string   `json:"image,omitempty"`
	IdleMinutes       int      `json:"idle_minutes,omitempty"`
	Setup             []string `json:"setup,omitempty"`
	Environment       []EnvVar `json:"environment,omitempty"`
	Webhook           string   `json:"webhook,omitempty"`
	ExternalReference string   `json:"external_reference,omitempty"`
	TimeoutMinutes    int      `json:"timeout_minutes,omitempty"`
}

type MachineUpdateRequest struct {
	Name              *string   `json:"name,omitempty"`
	Size              *string   `json:"size,omitempty"`
	IdleMinutes       *int      `json:"idle_minutes,omitempty"`
	Setup             *[]string `json:"setup,omitempty"`
	Environment       *[]EnvVar `json:"environment,omitempty"`
	Webhook           *string   `json:"webhook,omitempty"`
	ExternalReference *string   `json:"external_reference,omitempty"`
	TimeoutMinutes    *int      `json:"timeout_minutes,omitempty"`
}

type MachineStartRequest struct {
	Environment       []EnvVar `json:"environment,omitempty"`
	Webhook           string   `json:"webhook,omitempty"`
	ExternalReference string   `json:"external_reference,omitempty"`
}

type MachineHeartbeatRequest struct {
	LeaseID    string `json:"lease_id,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	Release    bool   `json:"release,omitempty"`
}

func (c *Client) Machines(ctx context.Context) ([]Machine, error) {
	var out struct {
		Machines []Machine `json:"machines"`
	}
	return out.Machines, c.do(ctx, "GET", "/v1/machines", nil, &out)
}

func (c *Client) CreateMachine(ctx context.Context, req MachineRequest) (Machine, error) {
	var m Machine
	return m, c.do(ctx, "POST", "/v1/machines", req, &m)
}

func (c *Client) Machine(ctx context.Context, id string) (Machine, error) {
	var m Machine
	return m, c.do(ctx, "GET", "/v1/machines/"+url.PathEscape(id), nil, &m)
}

func (c *Client) UpdateMachine(ctx context.Context, id string, req MachineUpdateRequest) (Machine, error) {
	var m Machine
	return m, c.do(ctx, "PATCH", "/v1/machines/"+url.PathEscape(id), req, &m)
}

func (c *Client) DeleteMachine(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/machines/"+url.PathEscape(id), nil, nil)
}

func (c *Client) StartMachine(ctx context.Context, id string) (Machine, error) {
	return c.StartMachineWith(ctx, id, MachineStartRequest{})
}

func (c *Client) StartMachineWith(ctx context.Context, id string, req MachineStartRequest) (Machine, error) {
	var m Machine
	return m, c.do(ctx, "POST", "/v1/machines/"+url.PathEscape(id)+"/start", req, &m)
}

func (c *Client) StopMachine(ctx context.Context, id string) (Machine, error) {
	var m Machine
	return m, c.do(ctx, "POST", "/v1/machines/"+url.PathEscape(id)+"/stop", nil, &m)
}

func (c *Client) SnapshotMachine(ctx context.Context, id string) (Image, error) {
	var img Image
	return img, c.do(ctx, "POST", "/v1/machines/"+url.PathEscape(id)+"/snapshot", nil, &img)
}

func (c *Client) HeartbeatMachine(ctx context.Context, id string, req MachineHeartbeatRequest) (Machine, error) {
	var m Machine
	return m, c.do(ctx, "POST", "/v1/machines/"+url.PathEscape(id)+"/heartbeat", req, &m)
}
