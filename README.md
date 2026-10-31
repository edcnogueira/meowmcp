# meowmcp

MCP server that gives LLM agents access to your personal WhatsApp account, built on
[whatsmeow](https://github.com/tulir/whatsmeow) (WhatsApp Web multi-device protocol) and the
official [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk).

The agent can log in by itself (it receives the QR code / pairing code and shows it to you), read
chats, search history, reply, start new conversations with any number and get notified when the other
side answers — e.g. "book me a dentist appointment" end to end.

> Unofficial project, not affiliated with WhatsApp or Meta. Automating a personal account is against
> WhatsApp's terms and may get the number banned; keep volumes human-like and use it for your own
> conversations.

## Tools

| Tool | What it does |
| --- | --- |
| `get_status` | Session state: `logged_out`, `pairing`, `connecting`, `connected`, `disconnected`, plus the current QR/pairing code while pairing and whether sends need approval (`send_approval`) |
| `login` | Starts pairing. Returns the QR code as PNG image, file path (`<data-dir>/qr.png`), raw payload and optional ASCII art; with `phone`, also an 8-character code for *Link with phone number* |
| `logout` | Unlinks the device |
| `list_chats` | Chats by recent activity with their last message; `query` filters by name/number |
| `get_chat_state` | Start-of-turn summary of one chat: last incoming and outgoing message, who it is waiting on, the unanswered incoming messages, the open question and the state of their attachments |
| `get_messages` | Messages of a chat (JID or phone number), oldest to newest, with `before`/`after` date filters and `since_id` to read only what arrived after a known message |
| `get_media` | Returns a message's image, sticker, voice note, audio, video or document. Images come back as an image the model sees; audio is transcribed only with `transcribe=true`; every type also returns the local `file_path` |
| `search_messages` | Text search across all chats or one chat, including voice-note transcripts |
| `search_contacts` | Address book + known chats by name, business name or number |
| `check_numbers` | Which numbers are on WhatsApp, their JIDs and verified business names |
| `send_message` | Sends text to a phone number or JID (starts new conversations too); optional quote-reply; echoes the text that went out |
| `send_file` | Sends a photo, video, audio, voice note or any document (with optional caption) from a server-side `file_path` or `data_base64`; optional quote-reply; echoes file name and caption |
| `wait_for_reply` | Blocks up to 240 s for incoming messages; only for answers expected within seconds (bots, codes) |
| `mark_as_read` | Sends read receipts for a chat |

Read-only tools carry `readOnlyHint`. None of them sends read receipts: `get_messages`, `get_chat_state`,
`get_media` and `wait_for_reply` return `marked_read: false`, and only `mark_as_read` marks anything.

### Messages

Every message has a `seq`: its local arrival order, strictly increasing. `get_messages` with `since_id` (a
`seq` or a previous `next_since_id`) returns what was stored after it, so two messages in the same second
or one delivered late with an older clock are never skipped, as a timestamp cursor would.

- `mentions` resolves each `@<number>` in `text` to the tagged person's JID, phone and name; `is_me` marks
  the user.
- `agent_name` holds the attendant a business account signs its messages with (`*Ana:*`); the signature is
  removed from `text`. Only messages from business accounts are rewritten.
- `media_status` is `ready` (stored on the server), `downloadable` (`get_media` fetches it from WhatsApp) or
  `missing_key` (old history synced without a download key: it cannot be fetched, ask the sender to resend).
- `view_once` photos, videos and audio are opened and stored as soon as they arrive; `get_media` returns the
  stored copy, also after WhatsApp stops serving it.
- `transcript_unclear` flags a low-confidence voice transcript (any Whisper segment with average log
  probability below -1, no-speech probability above 0.6 or compression ratio above 2.4): the agent must not
  fill in words.

Listings describe media by `media_type` + caption/file name; `get_media` fetches the content on demand.
Downloads land in `<data-dir>/media/<chat>/` and transcripts are stored with the message, so both happen
once per message and transcripts then show up in `get_messages` and `search_messages`. Images over 10 MB
and video/documents are returned by path only. Business messages with buttons or lists include their
options in `text` so the agent can pick one.

`send_file` picks the WhatsApp type from the file extension (content sniffing without one): JPEG/PNG go as
image, MP4/3GP as video, audio files as audio, anything else as a document; `send_as` overrides it
(`document` sends a photo uncompressed, `voice` turns an OGG/Opus file into a voice note). Files are capped
at 100 MB. `file_path` is read on the server host, so with the Docker deploy only files under the data dir
are reachable; send others as `data_base64` with `file_name`. Sent files are recorded in history and can
be fetched again with `get_media`.

Voice transcription uses any OpenAI-compatible `/audio/transcriptions` endpoint and never runs on its own:
only a `get_media` call with `transcribe=true` sends audio to it. It asks for `verbose_json` to get the
confidence and falls back to `json` (no `transcript_unclear`) for models that reject it. Without
`MEOWMCP_STT_API_KEY` audio is still downloaded and `transcription_error` says so.

### Watching a chat (events)

Waiting is not a tool call. The server implements the webhook delivery of the
[MCP events extension](https://github.com/modelcontextprotocol/experimental-ext-triggers-events/blob/main/docs/design-sketch-proposal.md)
(`events/list`, `events/subscribe`, `events/unsubscribe`) with one event type:

| Event | Arguments | Payload |
| --- | --- | --- |
| `chat.message_received` | `chat`: JID or phone number | `chat`, `messages` (same shape as `get_messages`), `since_id`, `has_more`, `marked_read: false` |

Incoming messages of the chat are gathered until 4 s pass without a new one (at most 30 s) and POSTed as
one event, signed per Standard Webhooks with the client's `whsec_` secret. The event `cursor` is the
`since_id` of its last message: a client that resubscribes with it after a restart gets what arrived
meanwhile (up to 24 h, or `maxAgeMs`). Subscriptions live in memory for the TTL the client asks (at most
24 h) and are renewed by subscribing again; callbacks must be `https`, answer a verification challenge
first and, unless `MEOWMCP_EVENTS_ALLOW_PRIVATE_CALLBACKS=true`, resolve to a public address (checked when
connecting, redirects are not followed). Over http the server only accepts subscriptions when
`MEOWMCP_AUTH_TOKEN` is set.

### Send approval

With `MEOWMCP_SEND_APPROVAL_SECRET` set, `send_message` and `send_file` refuse to send unless `approval` holds
a token for that exact recipient and content, minted by the client after the user approved it:

```
recipient = JID given in `to`, or <digits>@s.whatsapp.net for a phone number (as normalized by the server)
digest    = sha256hex(text)                                                      send_message
          = sha256hex("file\n" + sha256hex(bytes) + "\n" + file_name + "\n" + caption)   send_file
exp       = unix seconds, at most 5 minutes ahead
token     = "v1." + exp + "." + base64url_nopad(HMAC-SHA256(secret, "meowmcp-send-v1\n" + recipient + "\n" + digest + "\n" + exp))
```

Each token sends once (it is released again if WhatsApp did not take the message). `file_name` is the one
sent (default: base name of `file_path`) and `caption` is trimmed. Without the secret the server logs a
warning at startup and sends whatever it is asked to.

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
| `-send-approval-secret` | `MEOWMCP_SEND_APPROVAL_SECRET` | empty; sends need no approval. At least 32 characters |
| `-events-allow-private-callbacks` | `MEOWMCP_EVENTS_ALLOW_PRIVATE_CALLBACKS` | `false`; allow event webhooks to loopback, private and CGNAT (Tailscale) addresses |
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
default `~/services/meowmcp`) copies `compose.yaml` and the Hermes plugin
(`~/.hermes/plugins/meowmcp-approval`), generates `MEOWMCP_AUTH_TOKEN` and `MEOWMCP_SEND_APPROVAL_SECRET` in
the server-side `.env` on first run, mirrors both into `~/.hermes/.env`, records `MEOWMCP_TAG`, the data dir
and your uid/gid, pulls and restarts. Optional `.env` keys: `MEOWMCP_PORT`, `MEOWMCP_LOG_LEVEL`,
`MEOWMCP_EVENTS_ALLOW_PRIVATE_CALLBACKS` and the `MEOWMCP_STT_*` settings above. Logs:
`docker compose logs -f` in the deploy dir.

First install also needs the Hermes config below, `hermes plugins enable meowmcp-approval`, then
`systemctl --user restart hermes-gateway`.

## Hermes Agent

`~/.hermes/config.yaml`:

```yaml
mcp_servers:
  whatsapp:
    url: "http://127.0.0.1:8484/mcp"
    headers:
      Authorization: "Bearer ${MEOWMCP_AUTH_TOKEN}"
    timeout: 300
    trust: full
    events: true

platforms:
  webhook:
    enabled: true
    extra:
      public_url: "https://hermes.example.com"
```

`trust: full` is safe because the server, not Hermes, refuses unapproved sends: the `meowmcp-approval`
plugin (`integrations/hermes/meowmcp-approval`) hooks `pre_tool_call` on `mcp_whatsapp_send_message` and
`mcp_whatsapp_send_file`, shows you the recipient and the exact text (or file and caption) through Hermes'
approval prompt, and only after you approve adds a token valid for 2 minutes. A call that skips the plugin,
changes a word or reuses the token is refused by meowmcp. Set `MEOWMCP_HERMES_SERVER` if the
`mcp_servers` entry is not named `whatsapp`. Runs nobody can answer (cron, event runs) cannot send.

`events: true` gives the agent `subscribe_event`: "watch the chat with Sthefanny" becomes a subscription to
`chat.message_received` with `{"chat": "+55..."}`, and each reply starts a run whose prompt already holds
the new messages. Hermes needs its webhook listener behind HTTPS (`public_url`); when that address resolves
to a private or Tailscale IP, set `MEOWMCP_EVENTS_ALLOW_PRIVATE_CALLBACKS=true` in the server `.env`.

or spawned over stdio:

```yaml
mcp_servers:
  whatsapp:
    command: "/path/to/meowmcp"
    env:
      MEOWMCP_DATA_DIR: "${userHome}/.meowmcp"
      MEOWMCP_SEND_APPROVAL_SECRET: "${MEOWMCP_SEND_APPROVAL_SECRET}"
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
internal/providers/sttprovider        speech-to-text
internal/providers/webhookprovider    Standard Webhooks delivery for MCP events
internal/repositories/messagesrepo    SQLite chat/message history
internal/service/whatsappservice      transport-agnostic use cases (reused by a future HTTP API)
internal/transport/mcptransport       MCP tools and events
integrations/hermes/meowmcp-approval  Hermes plugin that mints send approvals
```

## Development

```sh
make test
```

## License

[Apache License 2.0](LICENSE). whatsmeow is licensed under [MPL-2.0](https://github.com/tulir/whatsmeow/blob/main/LICENSE).
