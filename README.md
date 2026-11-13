# Emoji Pounder

Bulk-upload local custom emoji to a Slack workspace with rate limiting,
automatic retries, and timestamped plain-text logging.

## Requirements

- Go 1.22 or newer
- A Slack **user token** belonging to a workspace user allowed to manage custom
  emoji
- PNG, JPG, JPEG, or GIF emoji files

Slack's documented `admin.emoji.add` method is Enterprise-only and accepts
public image URLs rather than local files. Emoji Pounder uses Slack's workspace
`emoji.add` multipart endpoint to support local files. This endpoint is not
part of Slack's documented public Web API, so Slack may change or remove it.
Bot tokens generally cannot manage workspace emoji.

Never commit a Slack token or put it in a shared shell-history file.

## Build

```shell
go build -o emoji-pounder .
```

## Usage

```shell
emoji-pounder -slacktoken "xoxp-..." -path "./emoji"
```

Validate the directory without contacting Slack:

```shell
emoji-pounder -slacktoken "unused" -path "./emoji" -dry-run
```

By default, only files directly inside the supplied directory are uploaded.
Use `-recursive` to include subdirectories.

Useful options:

- `-interval 3s`: minimum time between requests. The conservative default is
  approximately 20 requests per minute.
- `-max-retries 8`: retry limit for HTTP 429, server errors, timeouts, and
  Slack's transient API errors.
- `-timeout 60s`: per-request timeout.
- `-log emoji-pounder.log`: destination for human-readable, timestamped logs.
- `-endpoint URL`: alternate compatible endpoint, primarily for testing or
  future Slack API changes.

The program prints one summary line and writes the same totals to the log.
Detailed successes, every skip, failures, and retry delays are also logged.
Existing emoji (`already_exists`) are counted as skipped. The `errors` total
counts unsuccessful API or file-operation attempts, including transient errors
that succeeded after a retry, so it can be higher than `failed`. `Ctrl+C` stops
cleanly after cancelling the active request.

File names become Slack emoji names: they are lowercased, unsupported
characters become underscores, and the extension is removed. For example,
`Party Parrot.gif` becomes `party_parrot`.

Best used with the
[slack-emoji collection](https://github.com/scottish-terror/slack-emoji).
