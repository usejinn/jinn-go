// Package jinn is the Go client for Jinn's API (https://api.usejinn.com/v1). The
// command-line tool is a separate module, usejinn.com/jinn.
//
// A function is a published definition: a base, setup commands, a provider,
// a system prompt, tools and the files it returns. A run is one call of a
// function: a prompt and an optional input folder in, an output folder out.
//
//	c := jinn.New(os.Getenv("JINN_KEY"))
//	in, _ := c.UploadFolder(ctx, "./ticket")
//	run, _ := c.StartRun(ctx, "fnc_…", jinn.RunRequest{Prompt: "Answer this ticket.", Input: in})
//	run, _ = c.Wait(ctx, run.ID)
//	_ = c.DownloadOutput(ctx, run, "./result")
package jinn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is Jinn's API.
const DefaultBaseURL = "https://api.usejinn.com"

// Client calls the API with one account's key (jinn_…).
type Client struct {
	Key     string
	BaseURL string
	HTTP    *http.Client
}

// New returns a client for a key.
func New(key string) *Client {
	return &Client{Key: key, BaseURL: DefaultBaseURL, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// Error is the API's answer to a request it refused. Code is stable
// (no_credit, suspended, not_found, …; the API docs list them); Message is
// for people.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("jinn: %d %s: %s", e.Status, e.Code, e.Message) }

// do sends a JSON request and decodes the JSON answer into out.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "jinn-go")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		return &Error{Status: resp.StatusCode, Code: e.Code, Message: e.Error}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// ── Definitions ──────────────────────────────────────────────────────

// Definition is what a function version holds. Base, Provider, SystemPrompt,
// Size and TimeoutMinutes are required; a list left nil is empty.
type Definition struct {
	// Base is one of Jinn's disks (Bases lists them).
	Base string `json:"base"`
	// Setup runs before the agent, each command as root in its own
	// bash -euo pipefail -c, with network access and Environment.
	Setup []string `json:"setup"`
	// Provider names a provider version: prv_…@3 or prv_…@latest.
	Provider       string          `json:"provider"`
	SystemPrompt   string          `json:"system_prompt"`
	Tools          []string        `json:"tools"`
	CustomTools    []CustomTool    `json:"custom_tools"`
	InputManifest  []ManifestEntry `json:"input_manifest"`
	OutputManifest []ManifestEntry `json:"output_manifest"`
	Environment    []EnvVar        `json:"environment"`
	// Size is s, m, l or xl.
	Size string `json:"size"`
	// TimeoutMinutes is 1 to 1440; setup counts towards it.
	TimeoutMinutes int `json:"timeout_minutes"`
}

// ManifestEntry is a file, or a folder when Path ends in /, that must be
// in the input or output folder, at most MaxBytes.
type ManifestEntry struct {
	Path        string `json:"path"`
	Description string `json:"description"`
	MaxBytes    int64  `json:"max_bytes"`
}

// CustomTool is a tool you serve over HTTPS. Jinn POSTs the agent's
// arguments with the tool's secret as a bearer token. Send Secret once;
// later versions send the SecretID Jinn returned.
type CustomTool struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Parameters     json.RawMessage `json:"parameters"`
	URL            string          `json:"url"`
	Secret         string          `json:"secret,omitempty"`
	SecretID       string          `json:"secret_id,omitempty"`
	TimeoutSeconds int             `json:"timeout_seconds"`
}

// EnvVar is an environment variable of setup and the agent's commands: a
// Value, or a Secret sent once and kept by SecretID.
type EnvVar struct {
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Secret   string `json:"secret,omitempty"`
	SecretID string `json:"secret_id,omitempty"`
}

// Version is one published version of a function.
type Version struct {
	Function string `json:"function"`
	Version  int    `json:"version"`
	Definition
	ExternalReference string    `json:"external_reference,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	CreatedBy         string    `json:"created_by"`
}

// Function is a function's name and its latest version number.
type Function struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Latest    int       `json:"latest"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FunctionSummary is a function with its latest definition and last run.
type FunctionSummary struct {
	Function
	Definition Version `json:"definition"`
	Last       *Run    `json:"last"`
}

// Base is one of Jinn's disks.
type Base struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

func (c *Client) Bases(ctx context.Context) ([]Base, error) {
	var out struct {
		Bases []Base `json:"bases"`
	}
	return out.Bases, c.do(ctx, "GET", "/v1/bases", nil, &out)
}

func (c *Client) Functions(ctx context.Context) ([]FunctionSummary, error) {
	var out struct {
		Functions []FunctionSummary `json:"functions"`
	}
	return out.Functions, c.do(ctx, "GET", "/v1/functions", nil, &out)
}

// Function returns a function and its versions, oldest first.
func (c *Client) Function(ctx context.Context, id string) (Function, []Version, error) {
	var out struct {
		Function Function  `json:"function"`
		Versions []Version `json:"versions"`
	}
	err := c.do(ctx, "GET", "/v1/functions/"+url.PathEscape(id), nil, &out)
	return out.Function, out.Versions, err
}

// CreateFunction makes a function with a new id and its first version.
func (c *Client) CreateFunction(ctx context.Context, name string, def Definition) (Version, error) {
	var v Version
	body := struct {
		Name string `json:"name"`
		Definition
	}{name, def}
	return v, c.do(ctx, "POST", "/v1/functions", body, &v)
}

// Publish adds a function's next version.
func (c *Client) Publish(ctx context.Context, id string, def Definition) (Version, error) {
	var v Version
	return v, c.do(ctx, "POST", "/v1/functions/"+url.PathEscape(id)+"/versions", def, &v)
}

// ── Providers ────────────────────────────────────────────────────────

// Model is how runs talk to a model.
type Model struct {
	Provider        string `json:"provider"` // openai, anthropic or xai
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	CompactAtTokens int    `json:"compact_at_tokens"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
}

// ProviderVersion is one version of a provider. Its key is never returned.
type ProviderVersion struct {
	ID        string    `json:"id"`
	Version   int       `json:"version"`
	Name      string    `json:"name"`
	Model     Model     `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
}

// Provider is a provider and its versions, newest first.
type Provider struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Latest   int               `json:"latest"`
	Versions []ProviderVersion `json:"versions"`
}

// CatalogModel is a model a key can use.
type CatalogModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Efforts       []string `json:"efforts"`
	DefaultEffort string   `json:"default_effort,omitempty"`
}

func (c *Client) Providers(ctx context.Context) ([]Provider, error) {
	var out struct {
		Providers []Provider `json:"providers"`
	}
	return out.Providers, c.do(ctx, "GET", "/v1/providers", nil, &out)
}

// Catalog lists the models a vendor key can use; the key is kept nowhere.
func (c *Client) Catalog(ctx context.Context, vendor, key string) ([]CatalogModel, error) {
	var out struct {
		Models []CatalogModel `json:"models"`
	}
	return out.Models, c.do(ctx, "POST", "/v1/catalog", map[string]string{"provider": vendor, "key": key}, &out)
}

// CreateProvider makes a provider and its first version.
func (c *Client) CreateProvider(ctx context.Context, name string, m Model, key string) (ProviderVersion, error) {
	var p ProviderVersion
	return p, c.do(ctx, "POST", "/v1/providers", map[string]any{"name": name, "model": m, "key": key}, &p)
}

// PublishProvider adds a provider's next version: a new key, a new model
// or both.
func (c *Client) PublishProvider(ctx context.Context, id string, m Model, key string) (ProviderVersion, error) {
	var p ProviderVersion
	return p, c.do(ctx, "POST", "/v1/providers/"+url.PathEscape(id)+"/versions", map[string]any{"model": m, "key": key}, &p)
}
