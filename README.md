# Emoji Pounder

Bulk-upload local custom emoji to a Slack workspace with rate limiting,
automatic retries, and timestamped plain-text logging.

## Requirements

- Go 1.22 or newer
- PNG, JPG, JPEG, or GIF emoji files
- Credentials accepted by the configured emoji endpoint; see the compatibility
  warning below

## Important Slack API compatibility warning

Slack does not provide a supported custom-emoji upload API for apps installed
on Free, Pro, or Business+ workspaces. There is no `emoji:write` OAuth scope.
The workspace `emoji.add` endpoint used by this program is an undocumented
Slack web-client endpoint. Modern Slack rejects normal bot (`xoxb`) and user
OAuth (`xoxp`) app tokens at this endpoint.

On those plans, the endpoint requires browser-session authentication rather
than Slack App OAuth credentials. Browser authentication includes additional
session credentials that this program does not currently accept. Consequently,
the current version cannot upload emoji to a typical Free, Pro, or Business+
workspace using a token from a Slack App. Do not assume that granting more app
scopes will fix the authentication error.

Enterprise Grid provides the documented `admin.emoji.add` method. It requires:

- A **user token** with the `admin.teams:write` scope
- Installation across the Enterprise organization by an Org Admin or Owner
- Publicly reachable image URLs rather than direct local-file uploads

The current local-file multipart uploader does not implement that Enterprise
workflow. `emoji:read` only permits listing emoji and does not permit uploads.

Browser-session automation would be unofficial, security-sensitive, and
subject to breaking whenever Slack changes its web client. Never copy session
cookies or tokens into shared files, commit them, or expose them in logs.

Never commit a Slack token or put it in a shared shell-history file.

## Build

```shell
go build -o emoji-pounder .
```

## Usage

The following command applies only when using an endpoint that accepts the
provided bearer token. A normal Slack App token on Free, Pro, or Business+ will
not work:

```shell
emoji-pounder -slacktoken "<endpoint-token>" -path "./emoji"
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
