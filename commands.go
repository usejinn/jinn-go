package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"strconv"
	"time"
)

// Command kinds: the only way to act on a run. Every run accepts every kind.
const (
	KindBash   = "bash"
	KindRead   = "read"
	KindWrite  = "write"
	KindEdit   = "edit"
	KindList   = "list"
	KindPut    = "put"
	KindGet    = "get"
	KindFinish = "finish"
	// KindTerminal starts a shell; OpenTerminal sends it.
	KindTerminal = "terminal"
)

// Command states.
const (
	CommandQueued    = "queued"
	CommandRunning   = "running"
	CommandSucceeded = "succeeded"
	CommandFailed    = "failed"
	CommandCancelled = "cancelled"
)

// Command is one action on a run. It succeeds when the VM returned a result
// (a bash command that exits 1 succeeded, with exit_code 1); it fails when
// it could not run, and is cancelled when someone cancelled it or its run
// ended first.
type Command struct {
	ID        string          `json:"id"`
	Run       string          `json:"run"`
	Kind      string          `json:"kind"`
	Arguments json.RawMessage `json:"arguments"`
	// Source is caller, or agent for Jinn's agent's commands.
	Source string          `json:"source"`
	CallID string          `json:"call_id,omitempty"`
	State  string          `json:"state"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	// OutputBytes is how much a bash command has written so far.
	OutputBytes int64      `json:"output_bytes"`
	Sequence    int64      `json:"sequence"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   string     `json:"created_by"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	// DownloadURL is what a get delivered; ImageURL the image a read
	// showed. Both links work for 15 minutes.
	DownloadURL string `json:"download_url,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
}

// Done reports whether the command has ended.
func (c Command) Done() bool {
	return c.State == CommandSucceeded || c.State == CommandFailed || c.State == CommandCancelled
}

// Arguments of each kind.
type BashArgs struct {
	Command   string            `json:"command"`
	Workdir   string            `json:"workdir,omitempty"`
	TimeoutMS int64             `json:"timeout_ms,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

type ReadArgs struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type WriteArgs struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

type EditArgs struct {
	FilePath string     `json:"file_path"`
	Edits    []TextEdit `json:"edits"`
}

type TextEdit struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// PathArgs are list's and get's.
type PathArgs struct {
	Path string `json:"path"`
}

// PutArgs copy an upload (Upload's file_…) into the VM.
type PutArgs struct {
	File string `json:"file"`
	Path string `json:"path"`
}

// FinishArgs end a run: succeeded, or failed with a detail.
type FinishArgs struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// BashResult is a bash command's result: its exit code, the head and tail of
// its output, and how much it wrote.
type BashResult struct {
	ExitCode    int    `json:"exit_code"`
	Output      string `json:"output"`
	OutputBytes int64  `json:"output_bytes"`
	Truncated   bool   `json:"truncated,omitempty"`
	TimedOut    bool   `json:"timed_out,omitempty"`
	OOMKilled   bool   `json:"oom_killed,omitempty"`
	Error       string `json:"error,omitempty"`
}

// FinishResult is a finish's result. A succeeded finish whose output does
// not match the output manifest is not accepted, and the run goes on.
type FinishResult struct {
	Accepted bool     `json:"accepted"`
	Problems []string `json:"problems,omitempty"`
}

// SendCommand sends a command to a run. Commands run side by side; one sent
// while the run is queued starts when its setup has finished.
func (c *Client) SendCommand(ctx context.Context, run, kind string, args any) (Command, error) {
	var cmd Command
	err := c.do(ctx, "POST", "/v1/runs/"+url.PathEscape(run)+"/commands", map[string]any{"kind": kind, "arguments": args}, &cmd)
	return cmd, err
}

func waitParam(wait time.Duration) string {
	if wait <= 0 {
		return ""
	}
	return strconv.Itoa(int(min(wait, 25*time.Second) / time.Second))
}

// Command reads a command, waiting up to wait (at most 25 seconds) for it
// to end.
func (c *Client) Command(ctx context.Context, run, id string, wait time.Duration) (Command, error) {
	var cmd Command
	p := "/v1/runs/" + url.PathEscape(run) + "/commands/" + url.PathEscape(id)
	if w := waitParam(wait); w != "" {
		p += "?wait=" + w
	}
	return cmd, c.do(ctx, "GET", p, nil, &cmd)
}

// WaitCommand reads a command until it ends.
func (c *Client) WaitCommand(ctx context.Context, run, id string) (Command, error) {
	for {
		cmd, err := c.Command(ctx, run, id, 25*time.Second)
		if err != nil || cmd.Done() {
			return cmd, err
		}
		if ctx.Err() != nil {
			return cmd, ctx.Err()
		}
	}
}

// CommandPage is a run's commands that changed after a sequence, in the
// order they changed, about 4 MiB at most, the run's state, and the
// sequence to send as the next since.
type CommandPage struct {
	State    string    `json:"state"`
	Sequence int64     `json:"sequence"`
	Commands []Command `json:"commands"`
}

// Commands lists a run's commands that changed after since, waiting up to
// wait for a change.
func (c *Client) Commands(ctx context.Context, run string, since int64, wait time.Duration) (CommandPage, error) {
	var page CommandPage
	q := url.Values{"since": {strconv.FormatInt(since, 10)}}
	if w := waitParam(wait); w != "" {
		q.Set("wait", w)
	}
	return page, c.do(ctx, "GET", "/v1/runs/"+url.PathEscape(run)+"/commands?"+q.Encode(), nil, &page)
}

// OutputPage is a piece of a bash command's output, at most 512 KiB: Next
// is the offset to read from next; Done says the command ended and this is
// the last of its output.
type OutputPage struct {
	Output      string `json:"output"`
	Next        int64  `json:"next"`
	OutputBytes int64  `json:"output_bytes"`
	StoredBytes int64  `json:"stored_bytes"`
	Done        bool   `json:"done"`
}

// CommandOutput reads what a bash command wrote from an offset on, waiting
// up to wait for more.
func (c *Client) CommandOutput(ctx context.Context, run, id string, after int64, wait time.Duration) (OutputPage, error) {
	var page OutputPage
	q := url.Values{"after": {strconv.FormatInt(after, 10)}}
	if w := waitParam(wait); w != "" {
		q.Set("wait", w)
	}
	return page, c.do(ctx, "GET", "/v1/runs/"+url.PathEscape(run)+"/commands/"+url.PathEscape(id)+"/output?"+q.Encode(), nil, &page)
}

// CancelCommand cancels a command: a queued one at once, a running one when
// its host next hears.
func (c *Client) CancelCommand(ctx context.Context, run, id string) (Command, error) {
	var cmd Command
	return cmd, c.do(ctx, "POST", "/v1/runs/"+url.PathEscape(run)+"/commands/"+url.PathEscape(id)+"/cancel", nil, &cmd)
}

// Exec runs a bash command and waits for its result.
func (c *Client) Exec(ctx context.Context, run string, args BashArgs) (BashResult, error) {
	var res BashResult
	cmd, err := c.SendCommand(ctx, run, KindBash, args)
	if err == nil {
		cmd, err = c.WaitCommand(ctx, run, cmd.ID)
	}
	if err != nil {
		return res, err
	}
	if cmd.State != CommandSucceeded {
		return res, fmt.Errorf("jinn: the command %s: %s", cmd.State, cmd.Error)
	}
	return res, json.Unmarshal(cmd.Result, &res)
}

// Put copies a local file into a run at remote.
func (c *Client) Put(ctx context.Context, run, local, remote string) error {
	data, err := os.ReadFile(local)
	if err != nil {
		return err
	}
	file, err := c.Upload(ctx, data)
	if err != nil {
		return err
	}
	cmd, err := c.SendCommand(ctx, run, KindPut, PutArgs{File: file, Path: remote})
	if err == nil {
		cmd, err = c.WaitCommand(ctx, run, cmd.ID)
	}
	if err != nil {
		return err
	}
	return commandError(cmd)
}

// commandError is a command that did not do what it was sent to: it
// failed, was cancelled, or its result carries an error.
func commandError(cmd Command) error {
	if cmd.State != CommandSucceeded {
		return fmt.Errorf("jinn: the command %s: %s", cmd.State, cmd.Error)
	}
	var res struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(cmd.Result, &res)
	if res.Error != "" {
		return errors.New("jinn: " + res.Error)
	}
	return nil
}

// Get copies a file or a folder out of a run: a file to local, a folder
// unpacked into local. It reports whether remote was a folder.
func (c *Client) Get(ctx context.Context, run, remote, local string) (folder bool, err error) {
	cmd, err := c.SendCommand(ctx, run, KindGet, PathArgs{Path: remote})
	if err == nil {
		cmd, err = c.WaitCommand(ctx, run, cmd.ID)
	}
	if err != nil {
		return false, err
	}
	if err := commandError(cmd); err != nil {
		return false, err
	}
	var res struct {
		Folder bool `json:"folder"`
	}
	_ = json.Unmarshal(cmd.Result, &res)
	if cmd.DownloadURL == "" {
		return res.Folder, fmt.Errorf("jinn: %s is empty", path.Base(remote))
	}
	data, err := c.get(ctx, cmd.DownloadURL)
	if err != nil {
		return res.Folder, err
	}
	if res.Folder {
		return true, untarGz(data, local)
	}
	return false, os.WriteFile(local, data, 0o644)
}

// Finish ends a run: succeeded (its output checked and delivered, its disk
// saved if its function saves one), or failed with a detail. A succeeded
// finish whose output misses the manifest is not accepted: its result says
// why, and the run goes on.
func (c *Client) Finish(ctx context.Context, run string, args FinishArgs) (FinishResult, error) {
	var res FinishResult
	cmd, err := c.SendCommand(ctx, run, KindFinish, args)
	if err == nil {
		cmd, err = c.WaitCommand(ctx, run, cmd.ID)
	}
	if err != nil {
		return res, err
	}
	if cmd.State != CommandSucceeded {
		return res, fmt.Errorf("jinn: the finish %s: %s", cmd.State, cmd.Error)
	}
	return res, json.Unmarshal(cmd.Result, &res)
}

// WaitReady reads a run until its setup has finished, or it ended.
func (c *Client) WaitReady(ctx context.Context, id string) (Run, error) {
	for {
		r, err := c.Run(ctx, id)
		if err != nil || r.ReadyAt != nil || r.Done() {
			return r, err
		}
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// ── Images and tools ─────────────────────────────────────────────────

// Image is a disk a run can boot: one of the account's (img_…, saved by a
// run whose function saves its disk) or one of Jinn's bases (Base).
type Image struct {
	ID                string    `json:"id"`
	Base              bool      `json:"base,omitempty"`
	Run               string    `json:"run,omitempty"`
	Function          string    `json:"function,omitempty"`
	ExternalReference string    `json:"external_reference,omitempty"`
	SHA256            string    `json:"sha256"`
	Bytes             int64     `json:"bytes"`
	DiskBytes         int64     `json:"disk_bytes"`
	CreatedAt         time.Time `json:"created_at"`
}

// Images lists the account's images, newest first, then Jinn's bases.
func (c *Client) Images(ctx context.Context) ([]Image, error) {
	var out struct {
		Images []Image `json:"images"`
	}
	return out.Images, c.do(ctx, "GET", "/v1/images", nil, &out)
}

func (c *Client) Image(ctx context.Context, id string) (Image, error) {
	var img Image
	return img, c.do(ctx, "GET", "/v1/images/"+url.PathEscape(id), nil, &img)
}

// DeleteImage deletes one of the account's images. Runs that would boot it
// fail with image_not_found.
func (c *Client) DeleteImage(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/images/"+url.PathEscape(id), nil, nil)
}

// Tool is a standard tool as Jinn's agent shows it to its model.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Tools returns the standard tools' schemas: a loop of your own can show its
// model the same tools and send each call as the command of the same kind.
func (c *Client) Tools(ctx context.Context) ([]Tool, error) {
	var out struct {
		Tools []Tool `json:"tools"`
	}
	return out.Tools, c.do(ctx, "GET", "/v1/tools", nil, &out)
}
