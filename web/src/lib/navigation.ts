export function safeRedirect(target: string | undefined): string {
  return target &&
    target.startsWith("/") &&
    !target.startsWith("//") &&
    !target.includes("\\") &&
    !Array.from(target).some((char) => char.charCodeAt(0) <= 32)
    ? target
    : "/"
}
