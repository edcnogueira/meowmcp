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
| `search_messages` | Text search across all chats or one chat |
| `search_contacts` | Address book + known chats by name, business name or number |
| `check_numbers` | Which numbers are on WhatsApp, their JIDs and verified business names |
| `send_message` | Sends text to a phone number or JID (starts new conversations too); optional quote-reply |
| `wait_for_reply` | Blocks until the other side answers (or timeout), returning burst messages together |
| `mark_as_read` | Sends read receipts for a chat |

Read-only tools carry `readOnlyHint`, so clients that gate write actions (e.g. Hermes `trust: untrusted`)
ask for approval only for `login`, `logout`, `send_message` and `mark_as_read`.

Media is described (`media_type` + caption/file name) but not downloaded. Business messages with buttons
or lists include their options in `text` so the agent can pick one.

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

Logs go to stderr. The data dir holds `session.db` (whatsmeow keys — treat it like a password),
`messages.db` (local history) and `qr.png` while pairing.

### stdio vs http

Prefer **http** as a daemon: the session stays online, so messages land in local history as they
arrive and several agents can share one WhatsApp session. With stdio the session lives only while the
agent runs; messages received meanwhile are delivered by WhatsApp on the next connect, at the cost of a
reconnect on every agent start. Only one process may use a session at a time; a second one makes the
first go `disconnected` ("another process connected").

## Hermes Agent

`~/.hermes/config.yaml`:

```yaml
mcp_servers:
  whatsapp:
    url: "http://127.0.0.1:8484/mcp"
    headers:
      Authorization: "Bearer ${MEOWMCP_AUTH_TOKEN}"
    timeout: 300          # wait_for_reply blocks up to 240s
    trust: untrusted      # approve every send_message
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
