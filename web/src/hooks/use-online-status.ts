import { useEffect, useState } from "react"

export function useOnlineStatus(): boolean {
  const [online, setOnline] = useState(() =>
    typeof navigator === "undefined" ? true : navigator.onLine
  )

  useEffect(() => {
    const refresh = () =>
      setOnline(typeof navigator === "undefined" ? true : navigator.onLine)
    refresh()
    const markOnline = () => setOnline(true)
    const markOffline = () => setOnline(false)
    window.addEventListener("online", markOnline)
    window.addEventListener("offline", markOffline)
    window.addEventListener("pageshow", refresh)
    return () => {
      window.removeEventListener("online", markOnline)
      window.removeEventListener("offline", markOffline)
      window.removeEventListener("pageshow", refresh)
    }
  }, [])

  return online
}
