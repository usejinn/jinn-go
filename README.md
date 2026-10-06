# Jinn for Go

The Go client and the CLI for [Jinn](https://usejinn.com): run agent work as a call. A prompt and a folder in, a folder out.

This repository is published from Jinn's own source. Open issues here; changes come in from there.

## The CLI

```sh
curl -fsSL https://docs.usejinn.com/install.sh | sh
# or
go install usejinn.com/go/cmd/jinn@latest

jinn login
jinn run summarise --prompt "Summarise these notes." --in ./notes --out ./result
```

Every command is in the [CLI docs](https://docs.usejinn.com/cli).

## The SDK

```sh
go get usejinn.com/go
```

```go
c := jinn.New(os.Getenv("JINN_KEY"))
in, err := c.UploadFolder(ctx, "./ticket")
run, err := c.StartRun(ctx, "fnc_5d2a91c07e4b38f6a1d0c2e9", jinn.RunRequest{Prompt: "Answer this ticket.", Input: in})
run, err = c.Wait(ctx, run.ID)
if run.State == jinn.Succeeded {
	err = c.DownloadOutput(ctx, run, "./result")
}
```

`jinn.VerifyWebhook` checks a webhook's signature. The package has no dependencies outside the standard library.

Docs: [docs.usejinn.com](https://docs.usejinn.com) · API: [openapi.json](https://docs.usejinn.com/openapi.json) · Licence: MIT
