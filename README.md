# Jinn Go SDK

[![Go Reference](https://pkg.go.dev/badge/usejinn.com/go.svg)](https://pkg.go.dev/usejinn.com/go)

The Go client for the [Jinn](https://usejinn.com) API. A Jinn function says what a run is: the disk it boots, its setup, its size and, if you want one, an agent. A run with an agent takes a prompt and a folder and gives back the files you asked for. A run without one is a VM you drive with commands, and it can save its disk as an image for the next run.

It has no dependencies outside the standard library. The command-line tool is [jinn-cli](https://github.com/usejinn/jinn-cli).

## Install

```sh
go get usejinn.com/go
```

## Authenticate

An account owner makes an API key at [app.usejinn.com](https://app.usejinn.com) under **Account**. Pass it to `New`:

```go
import jinn "usejinn.com/go"

c := jinn.New(os.Getenv("JINN_KEY"))
```

## Run a function

```go
ctx := context.Background()

// Upload the input folder. It arrives in the run as /workspace/in.
in, err := c.UploadFolder(ctx, "./ticket")
if err != nil {
	log.Fatal(err)
}

run, err := c.StartRun(ctx, "fnc_5d2a91c07e4b38f6a1d0c2e9", jinn.RunRequest{
	Prompt:            "Answer this ticket.",
	Input:             in,
	ExternalReference: "ticket-4812",
})
if err != nil {
	log.Fatal(err)
}

run, err = c.Wait(ctx, run.ID) // reads the run every 5 seconds
if err != nil {
	log.Fatal(err)
}
if run.State != jinn.Succeeded {
	log.Fatalf("%s: %s", run.Failure, run.Detail)
}
err = c.DownloadOutput(ctx, run, "./result") // checks the SHA-256, then unpacks the .tar.gz
```

## Get the result by webhook

Start the run with a `Webhook` address. Jinn posts its events there: `run.running`, `run.maintenance`, `run.succeeded`, `run.failed` and `command.finished`. Check the signature with your account's public key (`whpk_…`, in the console):

```go
http.HandleFunc("/jinn", func(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	event, err := jinn.VerifyWebhook(publicKey, r.Header, body, time.Now())
	if err != nil {
		http.Error(w, "bad signature", http.StatusBadRequest)
		return
	}
	if event.Type == jinn.EventRunSucceeded {
		run, _ := event.Run()
		log.Printf("%s %s", event.Type, run.ID) // run.succeeded run_…
	}
})
```

## Drive a run yourself

A function without an agent is a VM you act on with commands. Commands run side by side; each waits for the VM's setup.

```go
run, err := c.StartRun(ctx, "fnc_…", jinn.RunRequest{Image: "img_…"}) // boot an image a run saved
if err != nil {
	log.Fatal(err)
}
res, err := c.Exec(ctx, run.ID, jinn.BashArgs{Command: "npm test", Workdir: "/workspace/app"})
if err != nil {
	log.Fatal(err)
}
fmt.Println(res.ExitCode, res.Output)

_ = c.Put(ctx, run.ID, "./fixture.json", "/workspace/app/fixture.json")
_, _ = c.Get(ctx, run.ID, "/workspace/app/coverage", "./coverage")

// Succeeded: /workspace/out is delivered and, if the function saves its
// disk, the run's output names the new image.
done, err := c.Finish(ctx, run.ID, jinn.FinishArgs{State: jinn.Succeeded})
```

`Tools` returns the standard tools' schemas, as Jinn's agent shows them to its model. A loop of your own can show its model the same tools and send each call as the command of the same kind.

## Reference

| Call | What it does |
|---|---|
| `Functions(ctx)` / `Function(ctx, id)` / `CreateFunction(ctx, name, def)` / `Publish(ctx, id, def)` / `DeleteFunction(ctx, id)` | Read, publish and delete functions. Each publish is a new version. |
| `StartRun(ctx, function, RunRequest)` | Start a run. Each call is a new run, unless it repeats `IdempotencyKey` (sent as the `Idempotency-Key` header): then it returns the first run. |
| `Run(ctx, id)` / `Wait(ctx, id)` / `WaitReady(ctx, id)` | Read a run; read it until it ends, or until its setup has finished. |
| `Runs(ctx, RunsQuery)` | List runs, newest first, a page at a time. |
| `Log(ctx, id)` | The run's log so far: setup, commands, the agent's messages and tool calls. |
| `LogSince(ctx, id, read)` | The events of the log's parts that `read` does not name yet; it adds them to `read`. Keep `read` to follow a run and read each part once. |
| `SendCommand(ctx, run, kind, args)` | Send a command: `bash`, `read`, `write`, `edit`, `list`, `put`, `get`, `terminal` or `finish`. |
| `Command(ctx, run, id, wait)` / `WaitCommand(ctx, run, id)` | Read a command; read it until it ends. |
| `Commands(ctx, run, since, wait)` | The run's commands that changed after a sequence. |
| `CommandOutput(ctx, run, id, after, wait)` | What a bash command has written so far. |
| `CancelCommand(ctx, run, id)` | Cancel a command. |
| `Exec`, `Put`, `Get`, `Finish` | Send a command of that kind and wait for its result. |
| `OpenTerminal(ctx, run, rows, cols, env)` | Start a shell in a run: the relay's address and route, and the end-to-end channel to seal and open its frames with (connect with any WebSocket client). |
| `Images(ctx)` / `Image(ctx, id)` / `DeleteImage(ctx, id)` | The account's images and Jinn's bases. |
| `Tools(ctx)` | The standard tools' schemas. |
| `Upload(ctx, data)` / `UploadFolder(ctx, dir)` | Upload a file, or a folder as a `.tar.gz`. Returns a `file_…` id. |
| `DownloadOutput(ctx, run, dir)` | Download and unpack a succeeded run's output folder. |
| `VerifyWebhook(publicKey, header, body, now)` | Check a webhook's signature and read its event. |
