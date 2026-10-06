// Command jinn is Jinn's CLI. It uses the same API as everything else.
//
//	jinn login                                   sign this machine in (a person approves it in the console)
//	jinn bases                                   list the bases a function can boot
//	jinn functions                               list the account's functions
//	jinn publish function.json                   make a function, or publish its next version
//	jinn run NAME|fnc_… -p PROMPT [--in DIR] [--out DIR] [--version N] [--webhook URL] [--ref REF] [--detach]
//	jinn runs [--function NAME|fnc_…] [--state active|succeeded|failed]
//	jinn show run_…                              print a run as JSON
//	jinn logs run_… [--follow]                   print a run's log
//	jinn output run_… --out DIR                  download a run's output folder
//	jinn providers                               list the account's providers
//	jinn models VENDOR                           list the models a vendor key can use (key on stdin)
//	jinn provider NAME|prv_… --vendor V --model M [--effort E] [--compact N] [--max-output N]
//	                                             make a provider, or publish its next version (key on stdin)
//
// The key comes from JINN_KEY, else ~/.config/jinn/key (jinn login writes it).
// JINN_API changes the API's address.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	jinn "usejinn.com/go"
)

const usage = `jinn: run agent work as a call.

  jinn login
  jinn bases
  jinn functions
  jinn publish function.json
  jinn run NAME|fnc_… --prompt PROMPT [--in DIR] [--out DIR] [--version N] [--webhook URL] [--ref REF] [--detach]
  jinn runs [--function NAME|fnc_…] [--state active|succeeded|failed]
  jinn show run_…
  jinn logs run_… [--follow]
  jinn output run_… --out DIR
  jinn providers
  jinn models openai|anthropic|xai                 < vendor-key
  jinn provider NAME|prv_… --vendor V --model M [--effort E] [--compact N] [--max-output N]   < vendor-key

Vendor keys are read from stdin, never from a flag: echo "$OPENAI_API_KEY" | jinn models openai
The key comes from JINN_KEY, else ~/.config/jinn/key. Docs: https://docs.usejinn.com/cli
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "login":
		err = login(ctx)
	case "bases":
		err = bases(ctx)
	case "functions":
		err = functions(ctx)
	case "publish":
		err = publish(ctx, args)
	case "run":
		err = run(ctx, args)
	case "runs":
		err = runs(ctx, args)
	case "show":
		err = show(ctx, args)
	case "logs":
		err = logs(ctx, args)
	case "output":
		err = output(ctx, args)
	case "providers":
		err = providers(ctx)
	case "models":
		err = models(ctx, args)
	case "provider":
		err = provider(ctx, args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "jinn: no command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "jinn:", strings.TrimPrefix(err.Error(), "jinn: "))
		os.Exit(1)
	}
}

func apiURL() string {
	if u := os.Getenv("JINN_API"); u != "" {
		return u
	}
	return jinn.DefaultBaseURL
}

func keyFile() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "jinn", "key")
}

func client() (*jinn.Client, error) {
	key := os.Getenv("JINN_KEY")
	if key == "" {
		b, err := os.ReadFile(keyFile())
		if err != nil {
			return nil, errors.New("no key: run jinn login, or set JINN_KEY")
		}
		key = strings.TrimSpace(string(b))
	}
	c := jinn.New(key)
	c.BaseURL = apiURL()
	return c, nil
}

func login(ctx context.Context) error {
	host, _ := os.Hostname()
	name := "cli " + strings.Map(func(r rune) rune {
		if strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 ._@:/-", r) {
			return r
		}
		return '-'
	}, host)
	if len(name) > 64 {
		name = name[:64]
	}
	l, err := jinn.StartLogin(ctx, apiURL(), name)
	if err != nil {
		return err
	}
	fmt.Printf("Open %s\nCheck that it shows %s, then approve.\n", l.URL, l.Code)
	ctx, cancel := context.WithDeadline(ctx, time.Unix(l.ExpiresAt, 0))
	defer cancel()
	key, err := l.Wait(ctx, apiURL())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyFile()), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(keyFile(), []byte(key+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Println("Signed in. The key is in", keyFile())
	return nil
}

func bases(ctx context.Context) error {
	c, err := client()
	if err != nil {
		return err
	}
	list, err := c.Bases(ctx)
	for _, b := range list {
		fmt.Printf("%s  %s\n", b.Name, size(b.Bytes))
	}
	return err
}

func functions(ctx context.Context) error {
	c, err := client()
	if err != nil {
		return err
	}
	list, err := c.Functions(ctx)
	for _, f := range list {
		last := "never run"
		if f.Last != nil {
			last = "last run " + f.Last.State + " " + f.Last.CreatedAt.Format(time.DateTime)
		}
		fmt.Printf("%-28s %-30s v%-4d %s\n", f.ID, f.Name, f.Latest, last)
	}
	return err
}

// functionID resolves a name to the account's one function of that name.
func functionID(ctx context.Context, c *jinn.Client, nameOrID string) (string, error) {
	if strings.HasPrefix(nameOrID, "fnc_") {
		return nameOrID, nil
	}
	list, err := c.Functions(ctx)
	if err != nil {
		return "", err
	}
	var ids []string
	for _, f := range list {
		if f.Name == nameOrID {
			ids = append(ids, f.ID)
		}
	}
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no function is named %s", nameOrID)
	case 1:
		return ids[0], nil
	}
	return "", fmt.Errorf("%d functions are named %s; use an id: %s", len(ids), nameOrID, strings.Join(ids, ", "))
}

// publish reads {"name": …, …definition} and publishes the next version of
// the account's function with that name, or makes it.
func publish(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: jinn publish function.json")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	var in struct {
		Name string `json:"name"`
		jinn.Definition
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return fmt.Errorf("%s: %w", args[0], err)
	}
	c, err := client()
	if err != nil {
		return err
	}
	id, err := functionID(ctx, c, in.Name)
	var v jinn.Version
	switch {
	case err == nil:
		v, err = c.Publish(ctx, id, in.Definition)
	case strings.HasPrefix(err.Error(), "no function"):
		v, err = c.CreateFunction(ctx, in.Name, in.Definition)
	}
	if err != nil {
		return err
	}
	fmt.Printf("→ %s %s v%d\n", in.Name, v.Function, v.Version)
	return nil
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: jinn run NAME|fnc_… --prompt PROMPT [--in DIR] [--out DIR]")
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "the agent's first message")
	fs.StringVar(prompt, "p", "", "the agent's first message")
	in := fs.String("in", "", "the input folder")
	out := fs.String("out", "", "where to put the output folder")
	version := fs.Int("version", 0, "the version (default: the latest)")
	webhook := fs.String("webhook", "", "where to send the result")
	ref := fs.String("ref", "", "your id for this run")
	detach := fs.Bool("detach", false, "print the run's id and return")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	fnc, err := functionID(ctx, c, args[0])
	if err != nil {
		return err
	}
	req := jinn.RunRequest{Prompt: *prompt, Version: *version, Webhook: *webhook, ExternalReference: *ref}
	if *in != "" {
		if req.Input, err = c.UploadFolder(ctx, *in); err != nil {
			return err
		}
	}
	r, err := c.StartRun(ctx, fnc, req)
	if err != nil {
		return err
	}
	fmt.Printf("%s queued · %s v%d\n", r.ID, args[0], r.Version)
	if *detach {
		return nil
	}
	r, err = follow(ctx, c, r)
	if err != nil {
		return err
	}
	if r.State != jinn.Succeeded {
		return fmt.Errorf("%s failed: %s: %s", r.ID, r.Failure, r.Detail)
	}
	fmt.Printf("✓ succeeded in %s\n", took(r))
	if *out != "" {
		if err := c.DownloadOutput(ctx, r, *out); err != nil {
			return err
		}
		for _, f := range r.Output.Files {
			if !f.Dir {
				fmt.Printf("%s %s\n", filepath.Join(*out, f.Path), size(f.Bytes))
			}
		}
	}
	return nil
}

// follow prints a run's log as it grows and returns the run once it ends.
func follow(ctx context.Context, c *jinn.Client, r jinn.Run) (jinn.Run, error) {
	seen, running := 0, false
	for {
		var err error
		if r, err = c.Run(ctx, r.ID); err != nil {
			return r, err
		}
		if r.State != jinn.Queued && !running {
			running = true
			fmt.Printf("%s running\n", r.ID)
		}
		if running {
			events, err := c.Log(ctx, r.ID)
			if err != nil {
				return r, err
			}
			for _, e := range events[min(seen, len(events)):] {
				printEvent(e)
			}
			seen = len(events)
		}
		if r.Done() {
			return r, nil
		}
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// printEvent prints what the agent did, one line each.
func printEvent(e jinn.LogEvent) {
	s := func(k string) string { v, _ := e.Fields[k].(string); return v }
	line := func(v string) string {
		v = strings.Join(strings.Fields(v), " ")
		if len(v) > 160 {
			v = v[:157] + "…"
		}
		return v
	}
	switch e.Kind {
	case "setup":
		code, _ := e.Fields["exit_code"].(float64)
		fmt.Printf("setup  %s  → %d\n", line(s("command")), int(code))
	case "text":
		fmt.Printf("agent  %s\n", line(s("text")))
	case "tool_call":
		fmt.Printf("agent  %s %s\n", s("name"), line(s("arguments")))
	case "tool_output":
		if failed, _ := e.Fields["error"].(bool); failed {
			fmt.Printf("       %s → %s\n", s("name"), line(s("output")))
		}
	case "retry":
		fmt.Printf("retry  %s\n", line(s("error")))
	}
}

func runs(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	function := fs.String("function", "", "one function's runs")
	state := fs.String("state", "", "active, succeeded or failed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	q := jinn.RunsQuery{State: *state}
	if *function != "" {
		if q.Function, err = functionID(ctx, c, *function); err != nil {
			return err
		}
	}
	list, _, err := c.Runs(ctx, q)
	for _, r := range list {
		fmt.Printf("%s  %-28s v%-3d %-9s %-15s %s  %s\n", r.ID, r.Function, r.Version, r.State, r.Failure, r.CreatedAt.Format(time.DateTime), took(r))
	}
	return err
}

func show(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: jinn show run_…")
	}
	c, err := client()
	if err != nil {
		return err
	}
	r, err := c.Run(ctx, args[0])
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	return nil
}

func logs(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: jinn logs run_… [--follow]")
	}
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	followFlag := fs.Bool("follow", false, "keep printing until the run ends")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	r, err := c.Run(ctx, args[0])
	if err != nil {
		return err
	}
	if *followFlag {
		r, err = follow(ctx, c, r)
		if err == nil {
			fmt.Printf("%s %s %s\n", r.ID, r.State, r.Failure)
		}
		return err
	}
	events, err := c.Log(ctx, r.ID)
	for _, e := range events {
		b, _ := json.Marshal(e.Fields)
		fmt.Printf("%s %-11s %s\n", e.At.Format("15:04:05"), e.Kind, b)
	}
	return err
}

func output(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: jinn output run_… --out DIR")
	}
	fs := flag.NewFlagSet("output", flag.ContinueOnError)
	out := fs.String("out", ".", "where to put the output folder")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	r, err := c.Run(ctx, args[0])
	if err != nil {
		return err
	}
	return c.DownloadOutput(ctx, r, *out)
}

func providers(ctx context.Context) error {
	c, err := client()
	if err != nil {
		return err
	}
	list, err := c.Providers(ctx)
	for _, p := range list {
		v := p.Versions[0]
		effort := v.Model.ReasoningEffort
		if effort == "" {
			effort = "-"
		}
		fmt.Printf("%-28s %-24s v%-3d %-9s %-28s %s\n", p.ID, p.Name, p.Latest, v.Model.Provider, v.Model.Model, effort)
	}
	return err
}

// vendorKey reads a vendor's API key from stdin, so it never sits in a
// flag, the shell's history or the process list.
func vendorKey() (string, error) {
	if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprint(os.Stderr, "Paste the vendor's API key, then press Enter: ")
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 8192))
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	if key == "" {
		return "", errors.New("no key on stdin: echo \"$KEY\" | jinn …")
	}
	return key, nil
}

func models(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: jinn models openai|anthropic|xai < vendor-key")
	}
	key, err := vendorKey()
	if err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	list, err := c.Catalog(ctx, args[0], key)
	for _, m := range list {
		efforts := strings.Join(m.Efforts, ",")
		if m.DefaultEffort != "" {
			efforts += " (default " + m.DefaultEffort + ")"
		}
		fmt.Printf("%-36s %s\n", m.ID, efforts)
	}
	return err
}

// providerID resolves a name to the account's one provider of that name.
func providerID(ctx context.Context, c *jinn.Client, nameOrID string) (string, error) {
	if strings.HasPrefix(nameOrID, "prv_") {
		return nameOrID, nil
	}
	list, err := c.Providers(ctx)
	if err != nil {
		return "", err
	}
	var ids []string
	for _, p := range list {
		if p.Name == nameOrID {
			ids = append(ids, p.ID)
		}
	}
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no provider is named %s", nameOrID)
	case 1:
		return ids[0], nil
	}
	return "", fmt.Errorf("%d providers are named %s; use an id: %s", len(ids), nameOrID, strings.Join(ids, ", "))
}

// provider makes a provider, or publishes the next version of the one
// named: a new key, a new model or both. Jinn checks the key and the model
// against the vendor's catalog.
func provider(ctx context.Context, args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: jinn provider NAME|prv_… --vendor V --model M [--effort E] [--compact N] [--max-output N] < vendor-key")
	}
	fs := flag.NewFlagSet("provider", flag.ContinueOnError)
	vendor := fs.String("vendor", "", "openai, anthropic or xai")
	model := fs.String("model", "", "the model's id (jinn models VENDOR lists them)")
	effort := fs.String("effort", "", "the reasoning effort, if the model takes one")
	compact := fs.Int("compact", 200000, "the history size, in tokens, at which a run compacts")
	maxOutput := fs.Int("max-output", 0, "the most tokens per reply (0: the model's maximum)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *vendor == "" || *model == "" {
		return errors.New("name the --vendor and the --model")
	}
	key, err := vendorKey()
	if err != nil {
		return err
	}
	c, err := client()
	if err != nil {
		return err
	}
	m := jinn.Model{Provider: *vendor, Model: *model, ReasoningEffort: *effort, CompactAtTokens: *compact, MaxOutputTokens: *maxOutput}
	id, err := providerID(ctx, c, args[0])
	var p jinn.ProviderVersion
	switch {
	case err == nil:
		p, err = c.PublishProvider(ctx, id, m, key)
	case strings.HasPrefix(err.Error(), "no provider"):
		p, err = c.CreateProvider(ctx, args[0], m, key)
	}
	if err != nil {
		return err
	}
	fmt.Printf("→ %s %s v%d · use %s@latest in a function's provider\n", p.Name, p.ID, p.Version, p.ID)
	return nil
}

func took(r jinn.Run) string {
	if r.StartedAt == nil || r.EndedAt == nil {
		return ""
	}
	return r.EndedAt.Sub(*r.StartedAt).Round(100 * time.Millisecond).String()
}

func size(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1f KB", float64(n)/1e3)
	}
	return fmt.Sprintf("%d B", n)
}
