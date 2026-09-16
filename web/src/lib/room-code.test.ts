import { describe, expect, it } from "vitest"

import { newRoomCode, normaliseRoomCode } from "./room-code"

describe("room codes", () => {
  it("generates 8-character unambiguous codes", () => {
    const code = newRoomCode()
    expect(code).toHaveLength(8)
    expect(code).toMatch(/^[23456789abcdefghjkmnpqrstuvwxyz]+$/)
    expect(code).not.toMatch(/[ilo01]/)
  })

  it("normalises user input aggressively", () => {
    expect(normaliseRoomCode("  K7M-2XW9Q! ")).toBe("k7m-2xw9q")
    expect(normaliseRoomCode("")).toBe("")
  })
})
