# meowmcp

MCP server that gives LLM agents access to your personal WhatsApp account, built on
[whatsmeow](https://github.com/tulir/whatsmeow) (WhatsApp Web multi-device protocol) and the
official [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk).

The agent can log in by itself (it receives the QR code / pairing code and shows it to you), read
chats, search history, reply, start new conversations with any number and wait for answers — e.g.
"book me a dentist appointment" end to end.

> Unofficial project, not affiliated with WhatsApp or Meta. Automating a personal account is against
> WhatsApp's terms and may get the number banned; keep volumes human-like and use it for your own
> conversations.

## Tools

| Tool | What it does |
| --- | --- |
| `get_status` | Session state: `logged_out`, `pairing`, `connecting`, `connected`, `disconnected`, plus the current QR/pairing code while pairing |
| `login` | Starts pairing. Returns the QR code as PNG image, file path (`<data-dir>/qr.png`), raw payload and optional ASCII art; with `phone`, also an 8-character code for *Link with phone number* |
| `logout` | Unlinks the device |
| `list_chats` | Chats by recent activity with their last message; `query` filters by name/number |
| `get_messages` | Messages of a chat (JID or phone number), oldest to newest, with `before`/`after` paging |
| `get_media` | Downloads a message's image, sticker, voice note, audio, video or document. Images come back as an image the model sees; audio is transcribed only with `transcribe=true`; every type also returns the local `file_path` |
| `search_messages` | Text search across all chats or one chat, including voice-note transcripts |
| `search_contacts` | Address book + known chats by name, business name or number |
| `check_numbers` | Which numbers are on WhatsApp, their JIDs and verified business names |
| `send_message` | Sends text to a phone number or JID (starts new conversations too); optional quote-reply |
| `send_file` | Sends a photo, video, audio, voice note or any document (with optional caption) from a server-side `file_path` or `data_base64`; optional quote-reply |
| `wait_for_reply` | Blocks until the other side answers (or timeout), returning burst messages together |
| `mark_as_read` | Sends read receipts for a chat |

Read-only tools carry `readOnlyHint`, so clients that gate write actions (e.g. Hermes `trust: untrusted`)
ask for approval only for `login`, `logout`, `send_message`, `send_file` and `mark_as_read`.

Listings describe media by `media_type` + caption/file name; `get_media` fetches the content on demand.
Downloads land in `<data-dir>/media/<chat>/` and transcripts are stored with the message, so both happen
once per message and transcripts then show up in `get_messages` and `search_messages`. Images over 10 MB
and video/documents are returned by path only. Media WhatsApp no longer serves (old history) fails with a
request to ask the sender to resend. Business messages with buttons or lists include their options in
`text` so the agent can pick one.

`send_file` picks the WhatsApp type from the file extension (content sniffing without one): JPEG/PNG go as
image, MP4/3GP as video, audio files as audio, anything else as a document; `send_as` overrides it
(`document` sends a photo uncompressed, `voice` turns an OGG/Opus file into a voice note). Files are capped
at 100 MB. `file_path` is read on the server host, so with the Docker deploy only files under the data dir
are reachable; send others as `data_base64` with `file_name`. Sent files are recorded in history and can
be fetched again with `get_media`.

Voice transcription uses any OpenAI-compatible `/audio/transcriptions` endpoint and never runs on its own:
only a `get_media` call with `transcribe=true` sends audio to it. Without `MEOWMCP_STT_API_KEY` audio is
still downloaded and `transcription_error` says so.

## Run

Requires Go 1.26+. No CGO (pure-Go SQLite).

```sh
go install github.com/edcnogueira/meowmcp/cmd/meowmcp@latest
```

or from source:

```sh
make build                       # ./bin/meowmcp
./bin/meowmcp -transport http    # long-running daemon on 127.0.0.1:8484/mcp
./bin/meowmcp                    # stdio, spawned by the MCP client
```

| Flag | Env | Default |
| --- | --- | --- |
| `-transport` | `MEOWMCP_TRANSPORT` | `stdio` (`stdio` or `http`) |
| `-addr` | `MEOWMCP_HTTP_ADDR` | `127.0.0.1:8484` |
| `-auth-token` | `MEOWMCP_AUTH_TOKEN` | empty; required when `-addr` is not loopback |
| `-data-dir` | `MEOWMCP_DATA_DIR` | `~/.meowmcp` |
| `-log-level` | `MEOWMCP_LOG_LEVEL` | `info` |
| `-stt-api-key` | `MEOWMCP_STT_API_KEY` | empty; transcription disabled |
| `-stt-url` | `MEOWMCP_STT_URL` | `https://api.groq.com/openai/v1` (OpenAI: `https://api.openai.com/v1`) |
| `-stt-model` | `MEOWMCP_STT_MODEL` | `whisper-large-v3-turbo` (OpenAI: `whisper-1`) |
| `-stt-language` | `MEOWMCP_STT_LANGUAGE` | empty, auto-detect; ISO-639-1 hint such as `pt` |

Logs go to stderr. The data dir holds `session.db` (whatsmeow keys — treat it like a password),
`messages.db` (local history), `media/` (downloaded attachments) and `qr.png` while pairing.

### stdio vs http

Prefer **http** as a daemon: the session stays online, so messages land in local history as they
arrive and several agents can share one WhatsApp session. With stdio the session lives only while the
agent runs; messages received meanwhile are delivered by WhatsApp on the next connect, at the cost of a
reconnect on every agent start. Only one process may use a session at a time; a second one makes the
first go `disconnected` ("another process connected").

## Deploy (Docker)

CI (`.github/workflows/ci.yml`) tests every push and publishes a multi-arch image to
`ghcr.io/edcnogueira/meowmcp`: `main` and `sha-<7 chars>` on every push to `main`, `X.Y.Z`, `X.Y` and
`latest` on `vX.Y.Z` tags. The image runs the http transport on `0.0.0.0:8484` with data in `/data`,
so `MEOWMCP_AUTH_TOKEN` is mandatory.

`deploy/compose.yaml` publishes the port on `127.0.0.1:8484` only, runs as your server user and
bind-mounts `<deploy dir>/data` at the same absolute path inside the container, so the `qr.png` path
returned by `login` is readable by Hermes on the host (and sendable as a `MEDIA:` attachment). The data
dir survives updates; deleting it unlinks the device.

From your machine, once CI for the pushed commit is green:

```sh
make deploy                    # install or update to :main
make deploy TAG=sha-1a2b3c4    # pin a commit / roll back
make deploy TAG=0.2.0          # pin a release
```

`deploy/deploy.sh` (host `MEOWMCP_DEPLOY_HOST`, default `edchomelab.local`; dir `MEOWMCP_DEPLOY_DIR`,
default `~/services/meowmcp`) copies `compose.yaml`, generates `MEOWMCP_AUTH_TOKEN` in the server-side
`.env` on first run, mirrors it into `~/.hermes/.env`, records `MEOWMCP_TAG`, the data dir and your
uid/gid, pulls and restarts. Optional `.env` keys: `MEOWMCP_PORT`, `MEOWMCP_LOG_LEVEL` and the
`MEOWMCP_STT_*` settings above. Logs: `docker compose logs -f` in the deploy dir.

First install also needs the `mcp_servers` entry below in `~/.hermes/config.yaml`, then
`systemctl --user restart hermes-gateway`.

## Hermes Agent

`~/.hermes/config.yaml`:

```yaml
mcp_servers:
  whatsapp:
    url: "http://127.0.0.1:8484/mcp"
    headers:
      Authorization: "Bearer ${MEOWMCP_AUTH_TOKEN}"
    timeout: 300          # wait_for_reply blocks up to 240s
    trust: untrusted      # approve every send_message / send_file
```

or spawned over stdio:

```yaml
mcp_servers:
  whatsapp:
    command: "/path/to/meowmcp"
    env:
      MEOWMCP_DATA_DIR: "${userHome}/.meowmcp"
```

First use: ask the agent to connect your WhatsApp. It calls `login`, sends you the QR (or, with your
number, the pairing code), and polls `get_status` until `connected`. History sync from the phone then
fills `list_chats`/`get_messages`.

## Layout

```
cmd/meowmcp                 wiring, flags, stdio/http serving
internal/config             flags + env
internal/domain             models and errors
internal/providers/whatsappprovider   whatsmeow session: pairing, reconnect, events -> domain
internal/repositories/messagesrepo    SQLite chat/message history
internal/service/whatsappservice      transport-agnostic use cases (reused by a future HTTP API)
internal/transport/mcptransport       MCP tools
```

## Development

```sh
make test
```

## License

[Apache License 2.0](LICENSE). whatsmeow is licensed under [MPL-2.0](https://github.com/tulir/whatsmeow/blob/main/LICENSE).
