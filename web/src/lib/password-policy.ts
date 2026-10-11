const MIN_PASSWORD_BYTES = 8
const MAX_PASSWORD_BYTES = 72

export function passwordUtf8Bytes(password: string): number {
  return new TextEncoder().encode(password).length
}

export function isPasswordLengthValid(password: string): boolean {
  const bytes = passwordUtf8Bytes(password)
  return bytes >= MIN_PASSWORD_BYTES && bytes <= MAX_PASSWORD_BYTES
}

export function passwordLengthMessage(password: string): string | null {
  const bytes = passwordUtf8Bytes(password)
  if (bytes < MIN_PASSWORD_BYTES) return "Password is too short."
  if (bytes > MAX_PASSWORD_BYTES) return "Password is too long."
  return null
}
