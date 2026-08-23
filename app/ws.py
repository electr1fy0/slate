"""In-memory WebSocket relay for live classroom boards.

Rooms hold the latest version of every scene element so late joiners get an
immediate snapshot. Element merges are last-writer-wins keyed on Excalidraw's
monotonic ``version`` field.

Connections must present a valid bearer JWT (``?token=``) and, when a browser
``Origin`` header is present, it must be allow-listed. Peers must produce
traffic (app-level ``ping`` counts) within ``WS_MAX_IDLE_S`` or a sweeper
evicts them; oversized payloads get an error frame followed by a disconnect.
"""

import asyncio
import json
import time
import uuid

import jwt
from fastapi import APIRouter, WebSocket, WebSocketDisconnect

import auth as board_auth
from config import settings

router = APIRouter()

WS_INVALID_TOKEN = 4401
WS_IDLE_TIMEOUT = 4402
WS_ORIGIN_FORBIDDEN = 4403
WS_PAYLOAD_TOO_LARGE = 1009

ERROR_MALFORMED_JSON = "MALFORMED_JSON"
ERROR_NOT_OBJECT = "FRAME_NOT_OBJECT"
ERROR_UNKNOWN_TYPE = "UNKNOWN_MESSAGE_TYPE"
ERROR_BAD_ELEMENTS = "BAD_ELEMENTS_PAYLOAD"
ERROR_TOO_LARGE = "PAYLOAD_TOO_LARGE"


def _merge_element(store: dict[str, dict], element: dict) -> bool:
    """Store ``element`` when it beats the currently known revision."""
    if not isinstance(element, dict):
        return False
    element_id = element.get("id")
    if not element_id:
        return False
    current = store.get(element_id)
    if current is None or element.get("version", 0) > current.get("version", 0):
        store[element_id] = element
        return True
    return False


class ConnectionManager:
    def __init__(self) -> None:
        self.rooms: dict[str, dict] = {}

    def room(self, room_id: str) -> dict:
        if room_id not in self.rooms:
            self.rooms[room_id] = {"peers": {}, "elements": {}}
        return self.rooms[room_id]

    def connect(self, room_id: str, websocket: WebSocket, name: str = "Guest",
                role: str = "student") -> str:
        peer_id = uuid.uuid4().hex[:12]
        self.room(room_id)["peers"][websocket] = {
            "id": peer_id,
            "name": (name or "Guest")[:40],
            "role": role if role in ("teacher", "student") else "student",
            "cursor": None,
            "last_seen": time.monotonic(),
        }
        return peer_id

    def identify(self, room_id: str, websocket: WebSocket, name: str, role: str) -> dict:
        peer = self.room(room_id)["peers"].get(websocket)
        if peer is not None:
            peer["name"] = (name or "Guest")[:40]
            peer["role"] = role if role in ("teacher", "student") else "student"
        return peer

    def touch(self, room_id: str, websocket: WebSocket) -> None:
        peer = self.room(room_id)["peers"].get(websocket)
        if peer is not None:
            peer["last_seen"] = time.monotonic()

    def disconnect(self, room_id: str, websocket: WebSocket) -> dict | None:
        room = self.rooms.get(room_id)
        if room is None:
            return None
        peer = room["peers"].pop(websocket, None)
        if not room["peers"]:
            self.rooms.pop(room_id, None)
        return peer

    def absorb(self, room_id: str, elements: list) -> int:
        store = self.room(room_id)["elements"]
        applied = 0
        for element in elements:
            if isinstance(element, dict) and _merge_element(store, element):
                applied += 1
        return applied

    def peers(self, room_id: str, exclude: WebSocket | None = None) -> list[dict]:
        return [peer for socket, peer in self.room(room_id)["peers"].items()
                if socket is not exclude]

    async def send(self, websocket: WebSocket, message: dict) -> None:
        await websocket.send_text(json.dumps(message))

    async def broadcast(self, room_id: str, message: dict,
                        exclude: WebSocket | None = None) -> None:
        room = self.rooms.get(room_id)
        if room is None:
            return
        for socket in list(room["peers"]):
            if socket is exclude:
                continue
            try:
                await self.send(socket, message)
            except Exception:
                self.disconnect(room_id, socket)

    async def evict_stale(self, max_idle_s: float) -> int:
        """Drop peers that have gone silent; returns how many were evicted."""
        now = time.monotonic()
        evicted = 0
        for room_id, room in list(self.rooms.items()):
            for socket in list(room["peers"].keys()):
                peer = room["peers"].get(socket)
                if peer is None or now - peer.get("last_seen", now) <= max_idle_s:
                    continue
                try:
                    await socket.close(code=WS_IDLE_TIMEOUT)
                except Exception:
                    pass
                left = self.disconnect(room_id, socket)
                evicted += 1
                if left is not None:
                    await self.broadcast(room_id, {"type": "peer-left", "peer": left})
        return evicted

    def reset(self) -> None:
        self.rooms.clear()


manager = ConnectionManager()


async def idle_evictor(stop_event: asyncio.Event, interval_s: float) -> None:
    """Background sweeper closing zombie sockets whose transport died quietly."""
    while not stop_event.is_set():
        try:
            await asyncio.wait_for(stop_event.wait(), timeout=interval_s)
        except TimeoutError:
            pass
        await manager.evict_stale(settings.ws_max_idle_s)


def resolve_socket_identity(websocket: WebSocket) -> dict | None:
    """Validate the connect JWT and load the live account behind it."""
    token = websocket.query_params.get("token", "")
    if not token:
        return None
    try:
        payload = board_auth.decode_token(token)
    except jwt.PyJWTError:
        return None
    user_id = payload.get("sub")
    if not user_id:
        return None
    user = board_auth.get_user(user_id)
    if user is None:
        return None
    role = user.get("role", "student")
    return {"name": user.get("name") or "Guest",
            "role": "student" if role == "student" else "teacher"}


def reject_origin(websocket: WebSocket) -> bool:
    """True when a browser origin is present but not allow-listed."""
    origin = websocket.headers.get("origin")
    return bool(origin) and settings.cors_origins and origin not in settings.cors_origins


@router.websocket("/ws/board/{room_id}")
async def board_socket(websocket: WebSocket, room_id: str) -> None:
    identity = resolve_socket_identity(websocket)
    if identity is None:
        await websocket.close(code=WS_INVALID_TOKEN)
        return
    if reject_origin(websocket):
        await websocket.close(code=WS_ORIGIN_FORBIDDEN)
        return
    await websocket.accept()
    peer_id = manager.connect(room_id, websocket, name=identity["name"],
                              role=identity["role"])
    try:
        while True:
            raw = await websocket.receive_text()
            manager.touch(room_id, websocket)
            if len(raw.encode("utf-8")) > settings.ws_max_message_bytes:
                await manager.send(websocket, {"type": "error", "code": ERROR_TOO_LARGE,
                                               "message": "frame too large"})
                await websocket.close(code=WS_PAYLOAD_TOO_LARGE)
                break
            try:
                message = json.loads(raw)
            except ValueError:
                await manager.send(websocket, {"type": "error", "code": ERROR_MALFORMED_JSON,
                                               "message": "frame is not valid JSON"})
                continue
            if not isinstance(message, dict):
                await manager.send(websocket, {"type": "error", "code": ERROR_NOT_OBJECT,
                                               "message": "frame must be a JSON object"})
                continue

            kind = message.get("type")
            if kind == "ping":
                # Liveness only; clients use the reply to gauge latency.
                await manager.send(websocket, {"type": "pong"})
            elif kind == "join":
                # Role always stays bound to the token; clients may only
                # refresh their display name.
                requested = str(message.get("name", "")) or identity["name"]
                manager.identify(room_id, websocket, requested, identity["role"])
                snapshot = list(manager.room(room_id)["elements"].values())
                await manager.send(websocket, {
                    "type": "init", "you": peer_id, "elements": snapshot,
                    "peers": manager.peers(room_id, exclude=websocket)})
                await manager.broadcast(
                    room_id, {"type": "peer-joined",
                              "peer": manager.room(room_id)["peers"][websocket]},
                    exclude=websocket)
            elif kind == "elements":
                incoming = message.get("elements")
                if not isinstance(incoming, list):
                    await manager.send(websocket, {"type": "error", "code": ERROR_BAD_ELEMENTS,
                                                   "message": "elements must be a list"})
                    continue
                if manager.absorb(room_id, incoming):
                    await manager.broadcast(
                        room_id, {"type": "elements", "elements": incoming},
                        exclude=websocket)
            elif kind == "cursor":
                peer = manager.room(room_id)["peers"].get(websocket)
                if peer is not None:
                    peer["cursor"] = {"x": message.get("x"), "y": message.get("y"),
                                      "tool": message.get("tool")}
                    await manager.broadcast(
                        room_id, {"type": "cursor", "peer": peer}, exclude=websocket)
            else:
                await manager.send(websocket, {"type": "error", "code": ERROR_UNKNOWN_TYPE,
                                               "message": f"unrecognised type {kind!r}"})
    except WebSocketDisconnect:
        pass
    finally:
        left = manager.disconnect(room_id, websocket)
        if left is not None:
            await manager.broadcast(room_id, {"type": "peer-left", "peer": left})
