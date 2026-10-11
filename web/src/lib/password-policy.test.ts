import { describe, expect, it } from "vitest"

import {
  isPasswordLengthValid,
  passwordLengthMessage,
  passwordUtf8Bytes,
} from "./password-policy"

describe("password length policy", () => {
  it("counts UTF-8 bytes and accepts only 8 through 72 bytes", () => {
    expect(passwordUtf8Bytes("12345678")).toBe(8)
    expect(isPasswordLengthValid("12345678")).toBe(true)
    expect(isPasswordLengthValid("1234567")).toBe(false)
    expect(isPasswordLengthValid("a".repeat(72))).toBe(true)
    expect(isPasswordLengthValid("a".repeat(73))).toBe(false)
    expect(passwordUtf8Bytes("é".repeat(36))).toBe(72)
    expect(isPasswordLengthValid("é".repeat(37))).toBe(false)
    expect(passwordLengthMessage("é".repeat(37))).toBe("Password is too long.")
  })
})
