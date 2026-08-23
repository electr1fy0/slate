import { Link, useRouterState } from "@tanstack/react-router"
import {
  ActivityIcon,
  ClipboardListIcon,
  HouseIcon,
  PenToolIcon,
  PresentationIcon,
} from "lucide-react"

import { useSession } from "@/lib/session"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
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
} from "@/components/ui/sidebar"

const NAV_ITEMS = [
  { label: "Home", to: "/", icon: HouseIcon },
  { label: "Live board", to: "/board", icon: PresentationIcon },
  { label: "Quizzes", to: "/teacher", icon: ClipboardListIcon },
  { label: "Ops console", to: "/ops", icon: ActivityIcon },
]

function initials(name: string): string {
  const trimmed = name.trim()
  if (!trimmed) return "??"
  return trimmed.slice(0, 2).toUpperCase()
}

export function AppSidebar() {
  const pathname = useRouterState({ select: (state) => state.location.pathname })
  const [session] = useSession()

  return (
    <Sidebar>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" render={<Link to="/" />}>
              <span className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
                <PenToolIcon />
              </span>
              <span className="flex flex-col gap-0.5 leading-tight">
                <span className="font-semibold">Slate</span>
                <span className="text-xs text-muted-foreground">classroom studio</span>
              </span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Studio</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {NAV_ITEMS.map((item) => {
                const active =
                  item.to === "/"
                    ? pathname === "/"
                    : pathname.startsWith(item.to)
                return (
                  <SidebarMenuItem key={item.to}>
                    <SidebarMenuButton
                      isActive={active}
                      tooltip={item.label}
                      render={<Link to={item.to} />}
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
            <div className="flex items-center gap-2 rounded-lg px-2 py-1.5">
              <Avatar className="size-8">
                <AvatarFallback>{initials(session.name)}</AvatarFallback>
              </Avatar>
              <div className="flex min-w-0 flex-1 flex-col leading-tight">
                <span className="truncate text-sm font-medium">
                  {session.name.trim() || "Anonymous"}
                </span>
                <span className="text-xs capitalize text-muted-foreground">
                  {session.role}
                </span>
              </div>
              <Badge variant={session.role === "teacher" ? "default" : "secondary"}>
                {session.role === "teacher" ? "Staff" : "Learner"}
              </Badge>
            </div>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>
  )
}
