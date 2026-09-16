import { useEffect, useState } from "react"

export function useOnlineStatus(): boolean {
  const [online, setOnline] = useState(() =>
    typeof navigator === "undefined" ? true : navigator.onLine,
  )

  useEffect(() => {
    const markOnline = () => setOnline(true)
    const markOffline = () => setOnline(false)
    window.addEventListener("online", markOnline)
    window.addEventListener("offline", markOffline)
    return () => {
      window.removeEventListener("online", markOnline)
      window.removeEventListener("offline", markOffline)
    }
  }, [])

  return online
}
