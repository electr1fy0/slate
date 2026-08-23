"""In-memory WebSocket relay for live classroom boards.

Rooms hold the latest version of every scene element so late joiners get an
immediate snapshot. Element merges are last-writer-wins keyed on Excalidraw's
monotonic ``version`` field.
"""

import json
import uuid

from fastapi import APIRouter, WebSocket, WebSocketDisconnect

router = APIRouter()


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

    def connect(self, room_id: str, websocket: WebSocket) -> str:
        peer_id = uuid.uuid4().hex[:12]
        self.room(room_id)["peers"][websocket] = {"id": peer_id, "name": "Guest",
                                                  "role": "student", "cursor": None}
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


@router.websocket("/ws/board/{room_id}")
async def board_socket(websocket: WebSocket, room_id: str) -> None:
    await websocket.accept()
    peer_id = manager.connect(room_id, websocket)
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
                manager.identify(room_id, websocket,
                                 str(message.get("name", "")), str(message.get("role", "")))
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
