package jinn

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Run states.
const (
	Queued    = "queued"
	Running   = "running"
	Succeeded = "succeeded"
	Failed    = "failed"
)

// Run is one run as the API returns it.
type Run struct {
	ID       string `json:"id"`
	Account  string `json:"account"`
	Function string `json:"function"`
	Version  int    `json:"version"`
	// Image is the disk the run booted: an img_… or a base's name.
	Image string `json:"image"`
	Size  string `json:"size"`
	// Agent says the function has an agent; Provider is its provider
	// version.
	Agent             bool       `json:"agent"`
	Provider          string     `json:"provider,omitempty"`
	Prompt            string     `json:"prompt,omitempty"`
	Input             string     `json:"input,omitempty"`
	Webhook           string     `json:"webhook,omitempty"`
	ExternalReference string     `json:"external_reference,omitempty"`
	State             string     `json:"state"`
	CreatedAt         time.Time  `json:"created_at"`
	CreatedBy         string     `json:"created_by"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	// ReadyAt is when setup finished and commands began.
	ReadyAt *time.Time `json:"ready_at,omitempty"`
	EndedAt *time.Time `json:"ended_at,omitempty"`
	// Failure is input, incomplete, timeout, provider, infrastructure,
	// setup, suspended, credit or maintenance; Detail says more.
	Failure string `json:"failure,omitempty"`
	Detail  string `json:"detail,omitempty"`
	// Message is the end of the agent's last text.
	Message string  `json:"message,omitempty"`
	Output  *Output `json:"output,omitempty"`
	Usage   *Usage  `json:"usage,omitempty"`
	// Sequence counts the run's changes, its commands' included.
	Sequence int64 `json:"sequence"`
	// MaintenanceAt is when Jinn will end the run to replace its host.
	MaintenanceAt *time.Time `json:"maintenance_at,omitempty"`
}

// Done reports whether the run has its result.
func (r Run) Done() bool { return r.State == Succeeded || r.State == Failed }

// Output is what a run returned: its output folder when it succeeded with
// files (one .tar.gz at URL, a link that works for 15 minutes from the read
// that returned it; Bytes and SHA256 are the .tar.gz's), and the image it
// saved, if its function saves one.
type Output struct {
	SHA256    string      `json:"sha256,omitempty"`
	Bytes     int64       `json:"bytes,omitempty"`
	Files     []FileEntry `json:"files,omitempty"`
	FileCount int         `json:"file_count,omitempty"`
	URL       string      `json:"url,omitempty"`
	Image     string      `json:"image,omitempty"`
}

type FileEntry struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Dir   bool   `json:"dir,omitempty"`
}

// Usage is what a run used: the VM's size, seconds and traffic, and the
// model's tokens.
type Usage struct {
	Compute struct {
		Size     string `json:"size"`
		Seconds  int64  `json:"seconds"`
		BytesIn  int64  `json:"bytes_in"`
		BytesOut int64  `json:"bytes_out"`
	} `json:"compute"`
	Model struct {
		Calls            int   `json:"calls"`
		InputTokens      int64 `json:"input_tokens"`
		CacheReadTokens  int64 `json:"cache_read_tokens"`
		CacheWriteTokens int64 `json:"cache_write_tokens"`
		OutputTokens     int64 `json:"output_tokens"`
		WebSearches      int   `json:"web_searches"`
	} `json:"model"`
}

// RunRequest starts a run. Version 0 is the function's latest. A function
// with an agent needs a Prompt; one without refuses it. Image boots another
// disk than the function's; Environment adds variables, or replaces the
// function's by name. IdempotencyKey goes as the Idempotency-Key header: a
// start of the same function with the same key in the last 24 hours returns
// that run instead of a new one.
type RunRequest struct {
	Version           int      `json:"version,omitempty"`
	Prompt            string   `json:"prompt,omitempty"`
	Image             string   `json:"image,omitempty"`
	Environment       []EnvVar `json:"environment,omitempty"`
	Input             string   `json:"input,omitempty"`
	Webhook           string   `json:"webhook,omitempty"`
	ExternalReference string   `json:"external_reference,omitempty"`
	IdempotencyKey    string   `json:"-"`
}

// StartRun starts a run of a function. Each call is a new run, unless it
// repeats an IdempotencyKey.
func (c *Client) StartRun(ctx context.Context, function string, in RunRequest) (Run, error) {
	var r Run
	var h http.Header
	if in.IdempotencyKey != "" {
		h = http.Header{"Idempotency-Key": {in.IdempotencyKey}}
	}
	return r, c.send(ctx, "POST", "/v1/functions/"+url.PathEscape(function)+"/runs", h, in, &r)
}

// Run reads a run.
func (c *Client) Run(ctx context.Context, id string) (Run, error) {
	var r Run
	return r, c.do(ctx, "GET", "/v1/runs/"+url.PathEscape(id), nil, &r)
}

// Wait reads a run every five seconds until it has its result.
func (c *Client) Wait(ctx context.Context, id string) (Run, error) {
	for {
		r, err := c.Run(ctx, id)
		if err != nil || r.Done() {
			return r, err
		}
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// RunsQuery filters a list of runs. A run key must name a Function.
type RunsQuery struct {
	Function string // fnc_…
	State    string // active, succeeded or failed
	Before   string // the Next of the previous page
}

// Runs lists runs newest first, a page at a time; next is "" on the last.
func (c *Client) Runs(ctx context.Context, q RunsQuery) (runs []Run, next string, err error) {
	v := url.Values{}
	for k, s := range map[string]string{"function": q.Function, "state": q.State, "before": q.Before} {
		if s != "" {
			v.Set(k, s)
		}
	}
	var out struct {
		Runs []Run  `json:"runs"`
		Next string `json:"next"`
	}
	err = c.do(ctx, "GET", "/v1/runs?"+v.Encode(), nil, &out)
	return out.Runs, out.Next, err
}

// LogEvent is one line of a run's log: what happened at At. The host writes
// disk, vm, input, setup, ready, command, command_end, image, traffic,
// result and host; the agent writes system, user, provider, reasoning, text,
// tool_call, tool_output, model, compacted and retry. Fields hold the rest.
type LogEvent struct {
	At     time.Time
	Kind   string
	Fields map[string]any
}

// logReaders is how many parts of a log Log reads at once: a long run's log
// is a thousand parts or more, and one at a time takes over a minute.
const logReaders = 8

// Log reads a run's log so far, in time order. A running run adds to it
// about every 30 seconds.
func (c *Client) Log(ctx context.Context, id string) ([]LogEvent, error) {
	return c.LogSince(ctx, id, map[string]bool{})
}

// LogSince reads the parts of a run's log that read does not name, adds
// them to read, and returns their events in time order. A part never
// changes once written, so a caller that follows a run keeps read and
// reads each part once. Parts are named by their path: the host and the
// agent write their own, so a new part can be listed before one already
// read, and its events can be older than ones read before.
func (c *Client) LogSince(ctx context.Context, id string, read map[string]bool) ([]LogEvent, error) {
	var parts struct {
		Parts []string `json:"parts"`
	}
	if err := c.do(ctx, "GET", "/v1/runs/"+url.PathEscape(id)+"/log", nil, &parts); err != nil {
		return nil, err
	}
	var urls, names []string
	for _, u := range parts.Parts {
		name := u
		if p, err := url.Parse(u); err == nil {
			name = p.Path
		}
		if !read[name] {
			urls, names = append(urls, u), append(names, name)
		}
	}
	texts, errs := make([][]byte, len(urls)), make([]error, len(urls))
	next := make(chan int)
	var wg sync.WaitGroup
	for range logReaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				texts[i], errs[i] = c.get(ctx, urls[i])
			}
		}()
	}
	for i := range urls {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	for _, name := range names {
		read[name] = true
	}
	var events []LogEvent
	for _, data := range texts {
		for _, line := range bytes.Split(data, []byte("\n")) {
			var m map[string]any
			if json.Unmarshal(line, &m) != nil {
				continue
			}
			e := LogEvent{Fields: m}
			e.Kind, _ = m["kind"].(string)
			if at, ok := m["at"].(string); ok {
				e.At, _ = time.Parse(time.RFC3339Nano, at)
			}
			delete(m, "kind")
			delete(m, "at")
			events = append(events, e)
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	return events, nil
}

// get downloads a link the API returned (no key: the link is signed).
func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("jinn: download: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// ── Files ────────────────────────────────────────────────────────────

// Upload sends a file (a run's input folder, as one .tar.gz, or a file a
// put command copies into a run) and returns its file id.
func (c *Client) Upload(ctx context.Context, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	var f struct {
		ID      string            `json:"id"`
		URL     string            `json:"url"`
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
	}
	if err := c.do(ctx, "POST", "/v1/files", map[string]any{"bytes": len(data), "sha256": hex.EncodeToString(sum[:])}, &f); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, f.Method, f.URL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	for k, v := range f.Headers {
		req.Header.Set(k, v)
	}
	req.ContentLength = int64(len(data))
	resp, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("jinn: upload: %s: %s", resp.Status, b)
	}
	return f.ID, nil
}

// UploadFolder packs a folder's regular files and folders (no links) as a
// .tar.gz and uploads it.
func (c *Client) UploadFolder(ctx context.Context, dir string) (string, error) {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	tw := tar.NewWriter(zw)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: rel + "/", Mode: 0o755})
		case d.Type().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: rel, Mode: 0o644, Size: int64(len(data))}); err != nil {
				return err
			}
			_, err = tw.Write(data)
			return err
		}
		return fmt.Errorf("%s is not a regular file or folder", rel)
	})
	if err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return c.Upload(ctx, b.Bytes())
}

// DownloadOutput unpacks a succeeded run's output folder into dir.
func (c *Client) DownloadOutput(ctx context.Context, r Run, dir string) error {
	if r.Output == nil || r.Output.URL == "" {
		return errors.New("jinn: the run has no output")
	}
	data, err := c.get(ctx, r.Output.URL)
	if err != nil {
		return err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != r.Output.SHA256 {
		return errors.New("jinn: the output does not match its SHA-256")
	}
	return untarGz(data, dir)
}

// untarGz unpacks a .tar.gz into dir: its regular files and folders, by
// clean paths inside it.
func untarGz(data []byte, dir string) error {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if name == "." || name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			f, err := os.Create(dst)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		}
	}
}
