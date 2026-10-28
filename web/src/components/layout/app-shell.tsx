import { Link } from "@tanstack/react-router"
import { AppSidebar } from "@/components/layout/app-sidebar"
import { QuickActions } from "@/components/quick-actions"
import { Separator } from "@/components/ui/separator"
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"

interface AppShellProps {
  title: string
  description?: string
  actions?: React.ReactNode
  children: React.ReactNode
  padded?: boolean
}

export function AppShell({
  title,
  description,
  actions,
  children,
  padded = true,
}: AppShellProps) {
  return (
    <SidebarProvider>
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-50 focus:rounded focus:bg-background focus:p-3"
      >
        Skip to content
      </a>
      <AppSidebar />
      <SidebarInset className="flex h-svh flex-col overflow-hidden">
        <header className="flex h-14 shrink-0 items-center gap-2 border-b bg-background px-4">
          <SidebarTrigger />
          <Separator
            orientation="vertical"
            className="mr-1 data-[orientation=vertical]:h-4"
          />
          <div className="flex min-w-0 flex-1 items-center justify-between gap-3">
            <div className="min-w-0">
              <h1 title={title} className="truncate text-sm font-semibold">
                {title}
              </h1>
              {description ? (
                <p
                  title={description}
                  className="truncate text-xs text-muted-foreground"
                >
                  {description}
                </p>
              ) : null}
            </div>
            <div className="flex min-w-0 items-center gap-2">
              <div className="hidden min-w-0 items-center gap-2 sm:flex">
                {actions}
              </div>
              <QuickActions />
            </div>
          </div>
        </header>
        <main
          id="main-content"
          tabIndex={-1}
          className={
            padded
              ? "flex-1 overflow-y-auto p-4 md:p-6"
              : "relative flex-1 overflow-hidden"
          }
        >
          {children}
        </main>
      </SidebarInset>
    </SidebarProvider>
  )
}

export function ShellLink({
  to,
  children,
}: {
  to: string
  children: React.ReactNode
}) {
  return (
    <Link
      to={to}
      className="text-sm text-muted-foreground transition-colors hover:text-foreground"
    >
      {children}
    </Link>
  )
}
