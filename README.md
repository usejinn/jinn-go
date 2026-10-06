# Jinn Go SDK

[![Go Reference](https://pkg.go.dev/badge/usejinn.com/go.svg)](https://pkg.go.dev/usejinn.com/go)

The Go client for the [Jinn](https://usejinn.com) API. Jinn runs agent work as a call: you send a prompt and a folder, and you get back the files you asked for.

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

Start the run with a `Webhook` address. Jinn posts the run there when it ends. Check the signature with your account's public key (`whpk_…`, in the console):

```go
http.HandleFunc("/jinn", func(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	event, err := jinn.VerifyWebhook(publicKey, r.Header, body, time.Now())
	if err != nil {
		http.Error(w, "bad signature", http.StatusBadRequest)
		return
	}
	log.Printf("%s %s", event.Type, event.Data.ID) // run.succeeded run_…
})
```

## Reference

| Call | What it does |
|---|---|
| `StartRun(ctx, function, RunRequest)` | Start a run. Every call is a new run. |
| `Run(ctx, id)` / `Wait(ctx, id)` | Read a run, or read it until it ends. |
| `Runs(ctx, RunsQuery)` | List runs, newest first, a page at a time. |
| `Log(ctx, id)` | The run's log so far: setup output, the agent's messages and tool calls. |
| `Upload(ctx, tarGz)` / `UploadFolder(ctx, dir)` | Upload an input folder as a `.tar.gz`. Returns a `file_…` id. |
| `DownloadOutput(ctx, run, dir)` | Download and unpack a succeeded run's output folder. |
| `Functions`, `Function`, `CreateFunction`, `Publish` | Read and publish functions. Each publish is a new version. |
| `Providers`, `Catalog`, `CreateProvider`, `PublishProvider` | Manage model providers and their keys. |
| `Bases(ctx)` | List the bases a function can boot. |
| `VerifyWebhook(publicKey, header, body, now)` | Check a webhook and return its event. |

A refused request returns a `*jinn.Error` with the HTTP `Status`, a stable `Code` and the API's `Message`. Switch on `Code`: for example `no_credit`, `suspended` or `not_found`. The [API docs](https://docs.usejinn.com/api#errors) list every code.

## Links

- [Docs](https://docs.usejinn.com): functions, runs, webhooks and examples.
- [API](https://docs.usejinn.com/api) and [OpenAPI](https://docs.usejinn.com/openapi.json).
- Licence: MIT.

This repository is published from Jinn's main source. Open issues here.
