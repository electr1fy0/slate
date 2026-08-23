import json

import pytest
from fastapi.testclient import TestClient

import main
from config import settings
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
def student_token(client):
    resp = client.post("/auth/register", json={
        "email": "ws-student@example.com", "password": "hunter2hunter2",
        "name": "Ws Student"})
    return resp.json()["access_token"]


@pytest.fixture()
def teacher_token(client):
    client.post("/admin/users", headers={"x-admin-token": settings.admin_token}, json={
        "email": "ws-teacher@example.com", "password": "hunter2hunter2",
        "name": "Ws Teacher", "role": "teacher"})
    login = client.post("/auth/login", json={
        "email": "ws-teacher@example.com", "password": "hunter2hunter2"}).json()
    return login["access_token"]


def board_url(room_id: str, token: str) -> str:
    return f"/ws/board/{room_id}?token={token}"


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


def test_connect_binds_name_and_clamps_unknown_roles_to_student():
    socket = object()
    manager.connect("room", socket, name="T" * 99, role="wizard")
    peer = manager.room("room")["peers"][socket]
    assert peer["name"] == "T" * 40
    assert peer["role"] == "student"


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


@pytest.mark.anyio
async def test_broadcast_on_unknown_room_is_a_noop():
    await manager.broadcast("never-existed", {"type": "ping"})
    assert manager.rooms == {}


def test_reset_clears_every_room():
    manager.connect("r1", object())
    manager.connect("r2", object())
    manager.reset()
    assert manager.rooms == {}


# --------------------------------------------------------------- auth gating

def test_connect_without_token_is_rejected(client):
    with pytest.raises(Exception):  # noqa: B017 - denial raises WebSocketDisconnect
        with client.websocket_connect("/ws/board/room-1"):
            pass
    assert "room-1" not in manager.rooms


def test_connect_with_garbage_token_is_rejected(client):
    with pytest.raises(Exception):  # noqa: B017
        with client.websocket_connect("/ws/board/room-1?token=junk"):
            pass
    assert "room-1" not in manager.rooms


def test_connect_with_token_missing_sub_claim_is_rejected(client):
    import time as time_mod

    import jwt as pyjwt

    claims = {"typ": "access", "exp": int(time_mod.time()) + 60}
    token = pyjwt.encode(claims, settings.jwt_secret, algorithm=settings.jwt_algorithm)
    with pytest.raises(Exception):  # noqa: B017
        with client.websocket_connect(f"/ws/board/room-1?token={token}"):
            pass
    assert "room-1" not in manager.rooms


def test_connect_with_refresh_token_is_rejected(client):
    tokens = client.post("/auth/register", json={
        "email": "ref@example.com", "password": "hunter2hunter2",
        "name": "Ref"}).json()
    with pytest.raises(Exception):  # noqa: B017
        with client.websocket_connect(
                f"/ws/board/room-1?token={tokens['refresh_token']}"):
            pass
    assert "room-1" not in manager.rooms


def test_connect_with_deleted_account_is_rejected(client, student_token):
    resp = client.get("/auth/me", headers={"Authorization": f"Bearer {student_token}"})
    user_id = resp.json()["user"]["user_id"]
    import db

    db.table().delete_item(Key={"pk": f"USER#{user_id}", "sk": "PROFILE"})
    with pytest.raises(Exception):  # noqa: B017
        with client.websocket_connect(board_url("room-1", student_token)):
            pass
    assert "room-1" not in manager.rooms


def test_connect_from_unknown_origin_is_rejected(client, student_token):
    with pytest.raises(Exception):  # noqa: B017
        with client.websocket_connect(board_url("room-1", student_token),
                                      headers={"Origin": "https://evil.example"}):
            pass
    assert "room-1" not in manager.rooms


def test_connect_from_allowed_origin_is_accepted(client, student_token):
    with client.websocket_connect(board_url("room-1", student_token),
                                  headers={"Origin": "http://localhost:3000"}) as a:
        send(a, type="join")
        assert recv(a)["type"] == "init"


# ---------------------------------------------------------- socket journeys

def test_join_receives_empty_init(client, teacher_token):
    with client.websocket_connect(board_url("room-1", teacher_token)) as a:
        send(a, type="join")
        init = recv(a)
        assert init["type"] == "init"
        assert init["peers"] == []
        assert len(init["you"]) == 12
        assert init["elements"] == []


def test_identity_comes_from_token_not_client_claims(client, teacher_token,
                                                     student_token):
    with client.websocket_connect(board_url("room-1", teacher_token)) as a:
        send(a, type="join")
        recv(a)
        with client.websocket_connect(board_url("room-1", student_token)) as b:
            # Even if the client lies about being a teacher, the token wins.
            send(b, type="join", name="Bob", role="teacher")
            init_b = recv(b)
            assert [p["name"] for p in init_b["peers"]] == ["Ws Teacher"]
            assert init_b["peers"][0]["role"] == "teacher"
            joined = recv(a)
            assert joined["type"] == "peer-joined"
            assert joined["peer"]["name"] == "Bob"
            assert joined["peer"]["role"] == "student"


def test_second_join_sees_first_peer_and_triggers_peer_joined(client, teacher_token,
                                                              student_token):
    with client.websocket_connect(board_url("room-1", teacher_token)) as a:
        send(a, type="join")
        recv(a)
        with client.websocket_connect(board_url("room-1", student_token)) as b:
            send(b, type="join")
            init_b = recv(b)
            assert [p["name"] for p in init_b["peers"]] == ["Ws Teacher"]
            joined = recv(a)
            assert joined["type"] == "peer-joined"
            assert joined["peer"]["name"] == "Ws Student"


def test_element_updates_relay_and_snapshot_for_late_joiners(client, teacher_token,
                                                             student_token):
    with client.websocket_connect(board_url("room-1", teacher_token)) as a:
        send(a, type="join")
        recv(a)
        with client.websocket_connect(board_url("room-1", student_token)) as b:
            send(b, type="join")
            recv(b)
            recv(a)
            send(a, type="elements", elements=[{"id": "e1", "version": 2}])
            relay = recv(b)
            assert relay == {"type": "elements",
                             "elements": [{"id": "e1", "version": 2}]}
            with client.websocket_connect(board_url("room-1", student_token)) as c:
                send(c, type="join")
                init_c = recv(c)
                assert init_c["elements"] == [{"id": "e1", "version": 2}]


def test_stale_elements_are_not_rebroadcast(client, teacher_token, student_token):
    with client.websocket_connect(board_url("room-1", teacher_token)) as a:
        send(a, type="join")
        recv(a)
        with client.websocket_connect(board_url("room-1", student_token)) as b:
            send(b, type="join")
            recv(b)
            recv(a)
            send(a, type="elements", elements=[{"id": "e1", "version": 5}])
            assert recv(b)["elements"][0]["version"] == 5
            send(a, type="elements", elements=[{"id": "e1", "version": 1}])
            send(a, type="elements", elements=[{"id": "e2", "version": 1}])
            relayed = recv(b)
            assert relayed["elements"] == [{"id": "e2", "version": 1}]


def test_cursor_broadcasts_sanitised_peer_state(client, teacher_token, student_token):
    with client.websocket_connect(board_url("room-1", teacher_token)) as a:
        send(a, type="join")
        recv(a)
        with client.websocket_connect(board_url("room-1", student_token)) as b:
            send(b, type="join")
            recv(b)
            recv(a)
            send(a, type="cursor", x=10, y=20, tool="rectangle")
            cursor = recv(b)
            assert cursor["type"] == "cursor"
            assert cursor["peer"]["name"] == "Ws Teacher"
            assert cursor["peer"]["cursor"] == {"x": 10, "y": 20, "tool": "rectangle"}


def test_disconnect_notifies_remaining_peers(client, teacher_token, student_token):
    with client.websocket_connect(board_url("room-1", teacher_token)) as a:
        send(a, type="join")
        recv(a)
        with client.websocket_connect(board_url("room-1", student_token)) as b:
            send(b, type="join")
            recv(b)
            recv(a)
        left = recv(a)
        assert left["type"] == "peer-left"
        assert left["peer"]["name"] == "Ws Student"


def test_malformed_messages_are_ignored_without_killing_socket(client, teacher_token):
    with client.websocket_connect(board_url("room-1", teacher_token)) as a:
        a.send_text("{definitely not json")
        a.send_text('"a bare string"')
        a.send_text("[1, 2, 3]")
        send(a, type="elements", elements="not-a-list")
        send(a, type="hover", whatever=True)
        send(a, type="join")
        assert recv(a)["type"] == "init"


def test_rooms_are_isolated_from_each_other(client, teacher_token, student_token):
    with client.websocket_connect(board_url("room-a", teacher_token)) as a:
        send(a, type="join")
        recv(a)
        with client.websocket_connect(board_url("room-b", student_token)) as b:
            send(b, type="join")
            assert recv(b)["peers"] == []


def test_admin_status_reports_active_board_rooms(client, student_token):
    with client.websocket_connect(board_url("ops-room", student_token)):
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
        query_params = {"token": "unused"}

        def __init__(self):
            self.headers = {}

        async def accept(self):
            pass

        async def receive_text(self):
            raise RuntimeError("client vanished without close frame")

    monkeypatch.setattr(manager, "broadcast", spy_broadcast)
    monkeypatch.setattr("ws.reject_origin", lambda websocket: False)
    monkeypatch.setattr("ws.resolve_socket_identity",
                        lambda websocket: {"name": "Racer", "role": "student"})

    def racing_disconnect(room_id, websocket):
        real_disconnect(room_id, websocket)
        return None

    monkeypatch.setattr(manager, "disconnect", racing_disconnect)
    from ws import board_socket

    with pytest.raises(RuntimeError):
        await board_socket(ScriptedSocket(), "race-room")
    assert events == []


def test_evicted_peer_disconnecting_again_is_harmless(client, teacher_token,
                                                      student_token, monkeypatch):
    """A socket whose outbound transport failed mid-session gets evicted by the
    broadcaster; when it later sends more messages or disconnects normally, the
    server must neither crash nor announce a phantom peer-left."""
    room = "flaky-room"
    with client.websocket_connect(board_url(room, teacher_token)) as a:
        send(a, type="join")
        init_a = recv(a)
        with client.websocket_connect(board_url(room, student_token)) as b:
            send(b, type="join")
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
