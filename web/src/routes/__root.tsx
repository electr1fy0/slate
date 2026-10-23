import { useState } from "react"
import {
  ClientOnly,
  HeadContent,
  Scripts,
  createRootRoute,
  Link,
} from "@tanstack/react-router"
import { TanStackRouterDevtoolsPanel } from "@tanstack/react-router-devtools"
import { TanStackDevtools } from "@tanstack/react-devtools"
import { QueryClientProvider } from "@tanstack/react-query"
import { RefreshCwIcon } from "lucide-react"

import { makeQueryClient } from "@/lib/query-client"
import { TooltipProvider } from "@/components/ui/tooltip"
import { Toaster } from "@/components/ui/sonner"
import { Button } from "@/components/ui/button"

import appCss from "../styles.css?url"

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: "utf-8" },
      { name: "viewport", content: "width=device-width, initial-scale=1" },
      { name: "referrer", content: "no-referrer" },
      {
        title: "Continuum — Assessment continuity for timed exams",
      },
      {
        name: "description",
        content:
          "Timed assessments with receipt-backed submissions and application failure recovery, powered by Continuum.",
      },
      { name: "theme-color", content: "#0f172a" },
      {
        property: "og:title",
        content: "Continuum — Assessment continuity for timed exams",
      },
      {
        property: "og:description",
        content:
          "Timed assessments with receipt-backed submissions and application failure recovery.",
      },
      { property: "og:type", content: "website" },
    ],
    links: [
      { rel: "stylesheet", href: appCss },
      { rel: "icon", href: "/favicon.ico", sizes: "any" },
      { rel: "icon", href: "/icon.svg", type: "image/svg+xml" },
      { rel: "manifest", href: "/manifest.json" },
    ],
  }),
  notFoundComponent: NotFound,
  errorComponent: RootError,
  shellComponent: RootDocument,
})

function NotFound() {
  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-3 p-6 text-center">
      <p className="text-7xl font-semibold tracking-tight text-primary">404</p>
      <p className="text-muted-foreground">That page isn't on the board.</p>
      <Button variant="outline" nativeButton={false} render={<Link to="/" />}>
        Back home
      </Button>
    </div>
  )
}

function RootError({ error }: { error: Error }) {
  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-3 p-6 text-center">
      <p className="text-5xl font-semibold tracking-tight text-destructive">
        Something broke
      </p>
      <p className="max-w-md text-sm text-muted-foreground">
        {error.message ||
          "An unexpected error occurred while rendering this page."}
      </p>
      <Button variant="outline" onClick={() => window.location.reload()}>
        <RefreshCwIcon data-icon="inline-start" />
        Reload
      </Button>
    </div>
  )
}

function Providers({ children }: { children: React.ReactNode }) {
  const [queryClient] = useState(() => makeQueryClient())
  return (
    <QueryClientProvider client={queryClient}>
      <TooltipProvider>{children}</TooltipProvider>
    </QueryClientProvider>
  )
}

function RootDocument({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        <HeadContent />
      </head>
      <body className="antialiased">
        <Providers>
          <ClientOnly
            fallback={
              <p role="status" className="p-6">
                Loading Continuum…
              </p>
            }
          >
            {children}
          </ClientOnly>
        </Providers>
        <Toaster position="bottom-right" />
        <noscript>
          Continuum needs JavaScript to open assessments and boards. Enable
          JavaScript and reload this page.
        </noscript>
        {import.meta.env.DEV && import.meta.env.CONTINUUM_DEMO !== "1" ? (
          <TanStackDevtools
            config={{ position: "bottom-right" }}
            plugins={[
              {
                name: "Tanstack Router",
                render: <TanStackRouterDevtoolsPanel />,
              },
            ]}
          />
        ) : null}
        <Scripts />
      </body>
    </html>
  )
}
