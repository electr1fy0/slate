import { Link, useRouterState } from "@tanstack/react-router"
import {
  ActivityIcon,
  ClipboardListIcon,
  HouseIcon,
  LogInIcon,
  LogOutIcon,
  PresentationIcon,
  SettingsIcon,
} from "lucide-react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { clearAuth, useAuth } from "@/lib/auth"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar"

const NAV_ITEMS = [
  { label: "Home", to: "/", icon: HouseIcon },
  { label: "Assessments", to: "/teacher", icon: ClipboardListIcon },
  { label: "Live board", to: "/board", icon: PresentationIcon },
  { label: "Continuum", to: "/ops", icon: ActivityIcon },
  { label: "Settings", to: "/settings", icon: SettingsIcon },
]

function initials(name: string): string {
  const trimmed = name.trim()
  if (!trimmed) return "??"
  return Array.from(trimmed).slice(0, 2).join("").toUpperCase()
}

export function AppSidebar() {
  const { setOpenMobile } = useSidebar()
  const pathname = useRouterState({
    select: (state) => state.location.pathname,
  })
  const auth = useAuth()
  const queryClient = useQueryClient()

  function signOut() {
    clearAuth()
    queryClient.clear()
    toast.info("Signed out")
  }

  return (
    <Sidebar>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" render={<Link to="/" />}>
              <span className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
                <ActivityIcon />
              </span>
              <span className="flex flex-col gap-0.5 leading-tight">
                <span className="font-semibold">Continuum</span>
                <span className="text-xs text-muted-foreground">
                  assessment continuity
                </span>
              </span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Assessment continuity</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {NAV_ITEMS.filter(
                (item) =>
                  auth.user?.role === "teacher" ||
                  (item.to !== "/teacher" && item.to !== "/ops")
              )
                .filter((item) => item.to !== "/settings" || auth.user)
                .map((item) => {
                  const active =
                    item.to === "/"
                      ? pathname === "/"
                      : pathname.startsWith(item.to)
                  return (
                    <SidebarMenuItem key={item.to}>
                      <SidebarMenuButton
                        isActive={active}
                        tooltip={item.label}
                        render={
                          <Link
                            to={item.to}
                            aria-current={active ? "page" : undefined}
                            onClick={() => setOpenMobile(false)}
                          />
                        }
                      >
                        <item.icon />
                        <span>{item.label}</span>
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  )
                })}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            {auth.user ? (
              <div className="flex items-center gap-2 rounded-lg px-2 py-1.5">
                <Avatar className="size-8">
                  <AvatarFallback>{initials(auth.user.name)}</AvatarFallback>
                </Avatar>
                <div className="flex min-w-0 flex-1 flex-col leading-tight">
                  <span className="truncate text-sm font-medium">
                    {auth.user.name}
                  </span>
                  <span className="truncate text-xs text-muted-foreground">
                    {auth.user.email}
                  </span>
                </div>
                <Badge
                  variant={
                    auth.user.role === "teacher" ? "default" : "secondary"
                  }
                >
                  {auth.user.role === "teacher" ? "Staff" : "Learner"}
                </Badge>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label="Sign out"
                  onClick={signOut}
                >
                  <LogOutIcon />
                </Button>
              </div>
            ) : (
              <SidebarMenuButton
                tooltip="Sign in"
                render={<Link to="/login" />}
              >
                <LogInIcon />
                <span>Sign in</span>
              </SidebarMenuButton>
            )}
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>
  )
}
