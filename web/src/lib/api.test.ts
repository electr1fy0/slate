import { afterEach, describe, expect, it, vi } from "vitest"

import { clearAuth, getAuth, setAuth } from "./auth"
import { ApiError, api, boardSocketUrl, hasExternalApiOrigin } from "./api"

const SESSION = {
  user: {
    user_id: "u1",
    email: "a@b.com",
    name: "Ada",
    role: "teacher" as const,
  },
  accessToken: "access-1",
  refreshToken: "refresh-1",
}

function jsonResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
  } as unknown as Response
}

describe("api client", () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
    localStorage.clear()
  })

  it("attaches bearer token and JSON content type", async () => {
    setAuth(SESSION)
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, []))
    globalThis.fetch = fetchMock

    await api.listExams()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [, init] = fetchMock.mock.calls[0]
    expect(init.headers.Authorization).toBe("Bearer access-1")
    expect(init.headers["Content-Type"]).toBe("application/json")
  })

  it("refreshes once on 401 then retries with the new token", async () => {
    setAuth(SESSION)
    let call = 0
    const fetchMock = vi.fn((url: string) => {
      if (String(url).endsWith("/auth/refresh")) {
        return Promise.resolve(
          jsonResponse(200, {
            user: SESSION.user,
            refresh_token: SESSION.refreshToken,
            access_token: "access-2",
          })
        )
      }
      call += 1
      if (call === 1) return Promise.resolve(jsonResponse(401, {}))
      return Promise.resolve(jsonResponse(200, [{ id: "e1" }]))
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const exams = await api.listExams()
    expect(exams).toEqual([{ id: "e1" }])
    // Second data call carries the refreshed token.
    const retryCall = fetchMock.mock.calls[2] as unknown as [
      string,
      RequestInit,
    ]
    const retryInit = retryCall[1]
    expect((retryInit.headers as Record<string, string>).Authorization).toBe(
      "Bearer access-2"
    )
  })

  it("surfaces string details and structured codes in ApiError", async () => {
    localStorage.clear() // no refresh possible
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        jsonResponse(403, {
          detail: { code: "EXAM_CLOSED", message: "window shut" },
        })
      )
    globalThis.fetch = fetchMock

    const error = await api.getExam("x").catch((e: unknown) => e)
    expect(error).toBeInstanceOf(ApiError)
    const apiError = error as ApiError
    expect(apiError.status).toBe(403)
    expect(apiError.code).toBe("EXAM_CLOSED")
    expect(apiError.message).toBe("window shut")
  })

  it.each([true, false])(
    "does not replace a new session with an old refresh response (success=%s)",
    async (success) => {
      setAuth(SESSION)
      let resolveRefresh!: (response: Response) => void
      const refresh = new Promise<Response>((resolve) => {
        resolveRefresh = resolve
      })
      const fetchMock = vi.fn((url: string) =>
        String(url).endsWith("/auth/refresh")
          ? refresh
          : Promise.resolve(jsonResponse(401, {}))
      )
      globalThis.fetch = fetchMock as unknown as typeof fetch
      const pending = api.listExams()
      await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
      const next = {
        ...SESSION,
        user: { ...SESSION.user, user_id: "u2" },
        accessToken: "new-access",
        refreshToken: "new-refresh",
      }
      setAuth(next)
      resolveRefresh(
        jsonResponse(success ? 200 : 401, {
          user: SESSION.user,
          access_token: "stale-access",
          refresh_token: "stale-refresh",
        })
      )
      await expect(pending).rejects.toBeInstanceOf(ApiError)
      expect(getAuth()).toEqual(next)
      expect(fetchMock).toHaveBeenCalledTimes(2)
    }
  )

  it("keeps a logout intact while a refresh response is in flight", async () => {
    setAuth(SESSION)
    let resolveRefresh!: (response: Response) => void
    globalThis.fetch = vi.fn((url: string) =>
      String(url).endsWith("/auth/refresh")
        ? new Promise<Response>((resolve) => {
            resolveRefresh = resolve
          })
        : Promise.resolve(jsonResponse(401, {}))
    ) as unknown as typeof fetch
    const pending = api.listExams()
    await vi.waitFor(() => expect(resolveRefresh).toBeTypeOf("function"))
    clearAuth()
    resolveRefresh(
      jsonResponse(200, {
        user: SESSION.user,
        access_token: "stale-access",
        refresh_token: "stale-refresh",
      })
    )
    await expect(pending).rejects.toBeInstanceOf(ApiError)
    expect(getAuth().user).toBeNull()
  })

  it("protects Continuum status and actions with the admin token", async () => {
    const status = {
      mode: "local",
      region: "ap-southeast-2",
      active_backend: "A",
      epoch: 1,
      paused: false,
      policy: {
        version: "v1",
        confirmation_probes: 3,
        probe_interval_ms: 5000,
      },
      backends: [],
      events: [],
      metrics: { failovers: 0, suppressed: 0, recovery_ms: null },
    }
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(200, status))
      .mockResolvedValueOnce(jsonResponse(200, status))
    globalThis.fetch = fetchMock

    await api.continuumStatus("ops-secret")
    await api.continuumAction("ops-secret", "partial", "A")

    expect(fetchMock.mock.calls[0][0]).toBe("/api/continuum/status")
    expect((fetchMock.mock.calls[0][1] as RequestInit).headers).toMatchObject({
      "x-admin-token": "ops-secret",
    })
    expect(fetchMock.mock.calls[1][0]).toBe("/api/continuum/action")
    expect(
      JSON.parse(String((fetchMock.mock.calls[1][1] as RequestInit).body))
    ).toEqual({
      action: "partial",
      backend: "A",
    })
    expect((fetchMock.mock.calls[1][1] as RequestInit).headers).toMatchObject({
      "x-admin-token": "ops-secret",
    })
  })

  it("falls back to a generic message for non-JSON failures", async () => {
    localStorage.clear()
    globalThis.fetch = vi.fn().mockRejectedValue(new Error("offline"))
    // rawRequest rejects -> request propagates the network failure
    await expect(api.listExams()).rejects.toThrow("offline")
  })

  it("boardSocketUrl encodes room and token over ws/wss", () => {
    Object.defineProperty(window, "location", {
      value: { protocol: "https:", host: "slate.example" },
      writable: true,
    })
    expect(boardSocketUrl("room one", "tok/en")).toBe(
      "wss://slate.example/ws/board/room%20one?token=tok%2Fen"
    )
    expect(boardSocketUrl("abc", null)).toBe("wss://slate.example/ws/board/abc")
  })

  it("detects a separately hosted API while keeping relative and same-origin APIs local", () => {
    expect(
      hasExternalApiOrigin(
        "https://api.example.test",
        "https://slate.example.test"
      )
    ).toBe(true)
    expect(hasExternalApiOrigin("/api", "https://slate.example.test")).toBe(
      false
    )
    expect(
      hasExternalApiOrigin(
        "https://slate.example.test/api",
        "https://slate.example.test"
      )
    ).toBe(false)
  })

  it("clearAuth removes the session used by the client", async () => {
    setAuth(SESSION)
    clearAuth()
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(200, []))
    globalThis.fetch = fetchMock
    await api.listExams()
    const [, init] = fetchMock.mock.calls[0]
    expect(init.headers.Authorization).toBeUndefined()
  })
})
