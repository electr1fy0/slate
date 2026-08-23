import json

import pytest
from fastapi.testclient import TestClient

import main
from ws import ConnectionManager, _merge_element, manager


@pytest.fixture(autouse=True)
def clean_rooms():
    manager.reset()
    yield
    manager.reset()


@pytest.fixture()
def client(make_table):
    make_table("continuum")
    with TestClient(main.app) as c:
        yield c


@pytest.fixture()
def anyio_backend():
    return "asyncio"


def send(socket, **message):
    socket.send_text(json.dumps(message))


def recv(socket):
    return json.loads(socket.receive_text())


# ------------------------------------------------------------- element merging

def test_merge_keeps_highest_version():
    store: dict = {}
    assert _merge_element(store, {"id": "e1", "version": 1}) is True
    assert _merge_element(store, {"id": "e1", "version": 1}) is False
    assert _merge_element(store, {"id": "e1", "version": 7}) is True
    assert _merge_element(store, {"id": "e1", "version": 2}) is False
    assert store["e1"]["version"] == 7


def test_merge_rejects_unidentifiable_elements():
    store: dict = {}
    assert _merge_element(store, {"version": 9}) is False
    assert _merge_element(store, "not-a-dict") is False
    assert store == {}


def test_absorb_counts_only_applied_elements():
    applied = manager.absorb("room", [
        {"id": "a", "version": 1},
        {"id": "a", "version": 0},
        {"bogus": True},
        "junk",
        {"id": "b", "version": 3},
    ])
    assert applied == 2
    assert set(manager.room("room")["elements"]) == {"a", "b"}


# ---------------------------------------------------------------- peer records

def test_connect_assigns_unique_ids():
    first = manager.connect("room", object())
    second = manager.connect("room", object())
    assert first != second
    assert len(first) == 12


def test_identify_normalises_name_and_role():
    socket = object()
    manager.connect("room", socket)
    peer = manager.identify("room", socket, "T" * 99, "teacher")
    assert peer["name"] == "T" * 40
    assert peer["role"] == "teacher"
    peer = manager.identify("room", socket, "", "wizard")
    assert peer["name"] == "Guest"
    assert peer["role"] == "student"


def test_identify_unknown_socket_returns_none():
    manager.connect("room", object())
    assert manager.identify("room", object(), "nobody", "student") is None


def test_disconnect_removes_peer_then_empty_room():
    socket = object()
    manager.connect("room", socket)
    other = object()
    manager.connect("room", other)
    assert manager.disconnect("room", object()) is None
    left = manager.disconnect("room", socket)
    assert left is not None and "id" in left
    assert "room" in manager.rooms
    manager.disconnect("room", other)
    assert "room" not in manager.rooms


def test_disconnect_unknown_room_is_none():
    assert manager.disconnect("ghost", object()) is None


def test_peers_excludes_requesting_socket():
    a, b = object(), object()
    manager.connect("room", a)
    manager.connect("room", b)
    assert len(manager.peers("room", exclude=a)) == 1


# ------------------------------------------------------------------ broadcasts

class FakeSocket:
    def __init__(self, fail=False):
        self.fail = fail
        self.sent = []

    async def send_text(self, text):
        if self.fail:
            raise RuntimeError("transport gone")
        self.sent.append(text)


@pytest.mark.anyio
async def test_broadcast_drops_dead_peers_and_delivers_to_live_ones():
    dead, live = FakeSocket(fail=True), FakeSocket()
    manager.room("room")["peers"][dead] = {"id": "dead"}
    manager.room("room")["peers"][live] = {"id": "live"}
    await manager.broadcast("room", {"type": "ping"})
    assert live.sent == ['{"type": "ping"}']
    assert list(manager.room("room")["peers"].values()) == [{"id": "live"}]


@pytest.mark.anyio
async def test_broadcast_skips_excluded_socket():
    a, b = FakeSocket(), FakeSocket()
    manager.room("room")["peers"][a] = {"id": "a"}
    manager.room("room")["peers"][b] = {"id": "b"}
    await manager.broadcast("room", {"type": "ping"}, exclude=a)
    assert a.sent == [] and len(b.sent) == 1


def test_reset_clears_every_room():
    manager.connect("r1", object())
    manager.connect("r2", object())
    manager.reset()
    assert manager.rooms == {}


# ------------------------------------------------------------- socket journeys

def test_join_receives_empty_init(client):
    with client.websocket_connect("/ws/board/room-1") as a:
        send(a, type="join", name="Ada", role="teacher")
        init = recv(a)
        assert init["type"] == "init"
        assert init["peers"] == []
        assert len(init["you"]) == 12
        assert init["elements"] == []


def test_second_join_sees_first_peer_and_triggers_peer_joined(client):
    with client.websocket_connect("/ws/board/room-1") as a:
        send(a, type="join", name="Ada", role="teacher")
        recv(a)
        with client.websocket_connect("/ws/board/room-1") as b:
            send(b, type="join", name="Bob", role="student")
            init_b = recv(b)
            assert [p["name"] for p in init_b["peers"]] == ["Ada"]
            assert init_b["peers"][0]["role"] == "teacher"
            joined = recv(a)
            assert joined["type"] == "peer-joined"
            assert joined["peer"]["name"] == "Bob"


def test_element_updates_relay_and_snapshot_for_late_joiners(client):
    with client.websocket_connect("/ws/board/room-1") as a:
        send(a, type="join", name="Ada", role="teacher")
        recv(a)
        with client.websocket_connect("/ws/board/room-1") as b:
            send(b, type="join", name="Bob", role="student")
            recv(b)
            recv(a)
            send(a, type="elements", elements=[{"id": "e1", "version": 2}])
            relay = recv(b)
            assert relay == {"type": "elements",
                             "elements": [{"id": "e1", "version": 2}]}
            with client.websocket_connect("/ws/board/room-1") as c:
                send(c, type="join", name="Cy", role="student")
                init_c = recv(c)
                assert init_c["elements"] == [{"id": "e1", "version": 2}]


def test_stale_elements_are_not_rebroadcast(client):
    with client.websocket_connect("/ws/board/room-1") as a:
        send(a, type="join", name="Ada", role="teacher")
        recv(a)
        with client.websocket_connect("/ws/board/room-1") as b:
            send(b, type="join", name="Bob", role="student")
            recv(b)
            recv(a)
            send(a, type="elements", elements=[{"id": "e1", "version": 5}])
            assert recv(b)["elements"][0]["version"] == 5
            send(a, type="elements", elements=[{"id": "e1", "version": 1}])
            send(a, type="elements", elements=[{"id": "e2", "version": 1}])
            relayed = recv(b)
            assert relayed["elements"] == [{"id": "e2", "version": 1}]


def test_cursor_broadcasts_sanitised_peer_state(client):
    with client.websocket_connect("/ws/board/room-1") as a:
        send(a, type="join", name="Ada", role="teacher")
        recv(a)
        with client.websocket_connect("/ws/board/room-1") as b:
            send(b, type="join", name="Bob", role="student")
            recv(b)
            recv(a)
            send(a, type="cursor", x=10, y=20, tool="rectangle")
            cursor = recv(b)
            assert cursor["type"] == "cursor"
            assert cursor["peer"]["name"] == "Ada"
            assert cursor["peer"]["cursor"] == {"x": 10, "y": 20, "tool": "rectangle"}


def test_disconnect_notifies_remaining_peers(client):
    with client.websocket_connect("/ws/board/room-1") as a:
        send(a, type="join", name="Ada", role="teacher")
        recv(a)
        with client.websocket_connect("/ws/board/room-1") as b:
            send(b, type="join", name="Bob", role="student")
            recv(b)
            recv(a)
        left = recv(a)
        assert left["type"] == "peer-left"
        assert left["peer"]["name"] == "Bob"


def test_malformed_messages_are_ignored_without_killing_socket(client):
    with client.websocket_connect("/ws/board/room-1") as a:
        a.send_text("{definitely not json")
        a.send_text('"a bare string"')
        a.send_text("[1, 2, 3]")
        send(a, type="elements", elements="not-a-list")
        send(a, type="hover", whatever=True)
        send(a, type="join", name="Ada", role="teacher")
        assert recv(a)["type"] == "init"


def test_rooms_are_isolated_from_each_other(client):
    with client.websocket_connect("/ws/board/room-a") as a:
        send(a, type="join", name="Ada", role="teacher")
        recv(a)
        with client.websocket_connect("/ws/board/room-b") as b:
            send(b, type="join", name="Bob", role="student")
            assert recv(b)["peers"] == []


def test_admin_status_reports_active_board_rooms(client):
    from config import settings

    with client.websocket_connect("/ws/board/ops-room"):
        body = client.get("/admin/status",
                          headers={"x-admin-token": settings.admin_token}).json()
        assert body["boards"] == {"active_rooms": 1}


# ------------------------------------------------------------------- hardening

@pytest.mark.anyio
async def test_socket_survives_disconnect_race(monkeypatch):
    """If a broadcast already evicted the socket, the finally-block must not
    attempt a peer-left notification for an already-gone peer."""
    events: list[str] = []
    real_disconnect = manager.disconnect
    original_broadcast = manager.broadcast

    async def spy_broadcast(room_id, message, exclude=None):
        events.append(message["type"])
        await original_broadcast(room_id, message, exclude=exclude)

    class ScriptedSocket:
        async def accept(self):
            pass

        async def receive_text(self):
            raise RuntimeError("client vanished without close frame")

    monkeypatch.setattr(manager, "broadcast", spy_broadcast)

    def racing_disconnect(room_id, websocket):
        real_disconnect(room_id, websocket)
        return None

    monkeypatch.setattr(manager, "disconnect", racing_disconnect)
    from ws import board_socket

    with pytest.raises(RuntimeError):
        await board_socket(ScriptedSocket(), "race-room")
    assert events == []


@pytest.mark.anyio
async def test_broadcast_on_unknown_room_is_a_noop():
    await manager.broadcast("never-existed", {"type": "ping"})
    assert manager.rooms == {}


def test_evicted_peer_disconnecting_again_is_harmless(client, monkeypatch):
    """A socket whose outbound transport failed mid-session gets evicted by the
    broadcaster; when it later sends more messages or disconnects normally, the
    server must neither crash nor announce a phantom peer-left."""
    room = "flaky-room"
    with client.websocket_connect(f"/ws/board/{room}") as a:
        send(a, type="join", name="Ada", role="teacher")
        init_a = recv(a)
        with client.websocket_connect(f"/ws/board/{room}") as b:
            send(b, type="join", name="Bob", role="student")
            recv(b)
            recv(a)
            sockets = {peer["id"]: sock for sock, peer
                       in manager.rooms[room]["peers"].items()}
            ada_socket = sockets[init_a["you"]]
            real_send = manager.send

            async def poisoned(socket, message):
                if socket is ada_socket:
                    raise RuntimeError("outbound transport died")
                await real_send(socket, message)

            monkeypatch.setattr(manager, "send", poisoned)
            send(b, type="elements", elements=[{"id": "e1", "version": 1}])
            send(a, type="elements", elements=[{"id": "e2", "version": 1}])
            assert recv(b)["elements"] == [{"id": "e2", "version": 1}]
            assert ada_socket not in manager.rooms[room]["peers"]
            send(a, type="cursor", x=1, y=2)
        # a's socket closes after eviction; the handler must exit cleanly
    assert room not in manager.rooms
