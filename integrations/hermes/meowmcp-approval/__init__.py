import base64
import binascii
import hashlib
import hmac
import os
import re
import time
from dataclasses import dataclass
from typing import Optional, Union

VALIDITY_SECONDS = 120
MAX_PROMPT_TEXT = 3000
_PHONE_FORMATTING = re.compile(r"[+ ()\-.]")


@dataclass
class _Send:
    recipient: str
    digest: str
    prompt: str


def _server_name() -> str:
    return re.sub(r"[^A-Za-z0-9_]", "_", os.environ.get("MEOWMCP_HERMES_SERVER", "whatsapp"))


def _tool_kind(tool_name: str) -> Optional[str]:
    prefix = f"mcp_{_server_name()}_"
    if tool_name == prefix + "send_message":
        return "text"
    if tool_name == prefix + "send_file":
        return "file"
    return None


def recipient_key(to: str) -> str:
    to = to.strip()
    if "@" in to:
        user, server = to.split("@", 1)
        user = user.split(":", 1)[0].split(".", 1)[0]
        if not user or not server:
            raise ValueError(f"invalid JID {to!r}")
        return f"{user}@{server}"
    digits = _PHONE_FORMATTING.sub("", to)
    if not digits.isdigit():
        raise ValueError(f"invalid phone number {to!r}")
    digits = digits.removeprefix("00")
    if not 8 <= len(digits) <= 15:
        raise ValueError(f"invalid phone number {to!r}")
    return digits + "@s.whatsapp.net"


def text_digest(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def file_digest(data: bytes, file_name: str, caption: str) -> str:
    return text_digest(f"file\n{hashlib.sha256(data).hexdigest()}\n{file_name}\n{caption}")


def mint(secret: str, recipient: str, digest: str, expires: int) -> str:
    message = f"meowmcp-send-v1\n{recipient}\n{digest}\n{expires}".encode("utf-8")
    mac = hmac.new(secret.encode("utf-8"), message, hashlib.sha256).digest()
    return f"v1.{expires}." + base64.urlsafe_b64encode(mac).decode("ascii").rstrip("=")


def _file_content(args: dict) -> tuple[bytes, str]:
    path = (args.get("file_path") or "").strip()
    file_name = (args.get("file_name") or "").strip()
    if path:
        with open(path, "rb") as f:
            data = f.read()
        return data, file_name or os.path.basename(path)
    encoded = "".join((args.get("data_base64") or "").split())
    return base64.b64decode(encoded, validate=True), file_name


def _describe(kind: str, args: dict) -> Union[_Send, str]:
    to = args.get("to") or ""
    try:
        recipient = recipient_key(to)
    except ValueError as exc:
        return f"WhatsApp send blocked: {exc}"
    if kind == "text":
        text = args.get("text") or ""
        shown = text if len(text) <= MAX_PROMPT_TEXT else text[:MAX_PROMPT_TEXT] + "\n[...]"
        return _Send(recipient, text_digest(text), f"Send this WhatsApp message to {to}?\n\n{shown}")
    try:
        data, file_name = _file_content(args)
    except (OSError, ValueError, binascii.Error) as exc:
        return f"WhatsApp send blocked: cannot read the file to approve it: {exc}"
    caption = (args.get("caption") or "").strip()
    prompt = f"Send the file {file_name} ({len(data)} bytes) to {to} on WhatsApp?"
    if caption:
        prompt += f"\n\nCaption:\n{caption}"
    return _Send(recipient, file_digest(data, file_name, caption), prompt)


def _approve(tool_name: str = "", args: Optional[dict] = None, **kwargs):
    kind = _tool_kind(tool_name)
    if kind is None:
        return None
    if not os.environ.get("MEOWMCP_SEND_APPROVAL_SECRET"):
        return {"action": "block", "message": "WhatsApp send blocked: MEOWMCP_SEND_APPROVAL_SECRET is not set for Hermes."}
    send = _describe(kind, args or {})
    if isinstance(send, str):
        return {"action": "block", "message": send}
    return {"action": "approve", "message": send.prompt, "rule_key": f"meowmcp-send:{send.digest[:16]}"}


def _sign(tool_name: str = "", args: Optional[dict] = None, **kwargs):
    kind = _tool_kind(tool_name)
    secret = os.environ.get("MEOWMCP_SEND_APPROVAL_SECRET")
    if kind is None or not secret:
        return None
    send = _describe(kind, args or {})
    if isinstance(send, str):
        return None
    token = mint(secret, send.recipient, send.digest, int(time.time()) + VALIDITY_SECONDS)
    return {"action": "modify", "args": {"approval": token}}


def register(ctx):
    ctx.register_hook("pre_tool_call", _approve)
    ctx.register_hook("pre_tool_call", _sign)
