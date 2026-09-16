import { redirect } from "@tanstack/react-router"

import { getAuth, isAuthenticated } from "./auth"

/**
 * Route guard for beforeLoad. Guarded routes run with `ssr: false`, so this
 * always executes in the browser where the persisted session is available.
 */
export function requireAuth(location: { href: string }): void {
  if (!isAuthenticated()) {
    throw redirect({
      to: "/login",
      search: { redirect: location.href },
      replace: true,
    })
  }
}

export function requireTeacher(location: { href: string }): void {
  requireAuth(location)
  if (getAuth().user?.role !== "teacher") {
    throw redirect({ to: "/", replace: true })
  }
}
