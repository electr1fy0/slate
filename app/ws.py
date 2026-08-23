"""In-memory WebSocket relay for live classroom boards.

Rooms hold the latest version of every scene element so late joiners get an
immediate snapshot. Element merges are last-writer-wins keyed on Excalidraw's
monotonic ``version`` field.

Connections must present a valid bearer JWT (``?token=``) and, when a browser
``Origin`` header is present, it must be allow-listed.
"""

import json
import uuid

import jwt
from fastapi import APIRouter, WebSocket, WebSocketDisconnect

import auth as board_auth
from config import settings

router = APIRouter()

WS_INVALID_TOKEN = 4401
WS_ORIGIN_FORBIDDEN = 4403


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
        }
        return peer_id

    def identify(self, room_id: str, websocket: WebSocket, name: str, role: str) -> dict:
        peer = self.room(room_id)["peers"].get(websocket)
        if peer is not None:
            peer["name"] = (name or "Guest")[:40]
            peer["role"] = role if role in ("teacher", "student") else "student"
        return peer

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

    def reset(self) -> None:
        self.rooms.clear()


manager = ConnectionManager()


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
            try:
                message = json.loads(raw)
            except ValueError:
                continue
            if not isinstance(message, dict):
                continue

            kind = message.get("type")
            if kind == "join":
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
    except WebSocketDisconnect:
        pass
    finally:
        left = manager.disconnect(room_id, websocket)
        if left is not None:
            await manager.broadcast(room_id, {"type": "peer-left", "peer": left})
