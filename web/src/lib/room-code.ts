const ALPHABET = "23456789abcdefghjkmnpqrstuvwxyz"

export function newRoomCode(): string {
  const bytes = new Uint8Array(8)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (byte) => ALPHABET[byte % ALPHABET.length]).join("")
}

export function normaliseRoomCode(raw: string): string {
  return raw.trim().toLowerCase().replace(/[^a-z0-9-]/g, "").slice(0, 32)
}
