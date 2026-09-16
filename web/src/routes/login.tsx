import { useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { LogInIcon, PenToolIcon, UserPlusIcon } from "lucide-react"

import { api, ApiError } from "@/lib/api"
import { setAuth } from "@/lib/auth"
import { AppShell } from "@/components/layout/app-shell"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { InputGroup, InputGroupInput } from "@/components/ui/input-group"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

type LoginSearch = { redirect?: string }

export const Route = createFileRoute("/login")({
  validateSearch: (search: Record<string, unknown>): LoginSearch => ({
    redirect: typeof search.redirect === "string" && search.redirect.startsWith("/")
      ? search.redirect
      : undefined,
  }),
  component: LoginPage,
  head: () => ({ meta: [{ title: "Sign in · Slate" }] }),
})

function safeRedirect(target: string | undefined): string {
  return target && target.startsWith("/") && !target.startsWith("//") ? target : "/"
}

export function validateLoginInput(email: string, password: string): boolean {
  if (!email.trim() || !password) {
    toast.error("Email and password are required")
    return false
  }
  return true
}

function LoginPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { redirect: redirectTo } = Route.useSearch()
  const [mode, setMode] = useState("signin")
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [name, setName] = useState("")
  const [pending, setPending] = useState(false)

  function finish(tokens: Awaited<ReturnType<typeof api.login>>) {
    setAuth({
      user: tokens.user,
      accessToken: tokens.access_token,
      refreshToken: tokens.refresh_token,
    })
    void queryClient.invalidateQueries()
    toast.success(mode === "signin" ? `Welcome back, ${tokens.user.name}` : "Account created")
    void navigate({ to: safeRedirect(redirectTo) })
  }

  async function submit() {
    /* c8 ignore next */
    if (!validateLoginInput(email, password)) return
    setPending(true)
    try {
      const tokens = mode === "signin"
        ? await api.login({ email: email.trim(), password })
        : await api.register({ email: email.trim(), password, name: name.trim() })
      finish(tokens)
    } catch (error) {
      toast.error(error instanceof ApiError ? error.message : "Authentication failed")
    } finally {
      setPending(false)
    }
  }

  const emailReady = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())
  const passwordReady = password.length >= 8
  const canSubmit = emailReady && (mode === "signin" || name.trim().length > 0) &&
    (mode === "signin" || passwordReady) && !pending

  return (
    <AppShell title="Sign in" description="Your account powers boards and quizzes">
      <div className="mx-auto w-full max-w-md pt-6">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <PenToolIcon data-icon="inline-start" />
              Slate account
            </CardTitle>
            <CardDescription>
              Students sign in to take quizzes; teachers sign in to run the class.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Tabs value={mode} onValueChange={(value) => setMode(value === "register" ? "register" : "signin")}>
              <TabsList className="w-full">
                <TabsTrigger value="signin" className="flex-1">Sign in</TabsTrigger>
                <TabsTrigger value="register" className="flex-1">Create account</TabsTrigger>
              </TabsList>
              <TabsContent value={mode}>
                <FieldGroup className="pt-2">
                  {mode === "register" ? (
                    <Field data-invalid={name.length > 0 && !name.trim() ? true : undefined}>
                      <FieldLabel htmlFor="login-name">Display name</FieldLabel>
                      <InputGroup>
                        <InputGroupInput
                          id="login-name"
                          placeholder="Ada Lovelace"
                          value={name}
                          onChange={(event) => setName(event.target.value)}
                          autoComplete="name"
                        />
                      </InputGroup>
                    </Field>
                  ) : null}
                  <Field data-invalid={email.length > 0 && !emailReady ? true : undefined}>
                    <FieldLabel htmlFor="login-email">Email</FieldLabel>
                    <InputGroup>
                      <InputGroupInput
                        id="login-email"
                        type="email"
                        placeholder="you@example.com"
                        value={email}
                        onChange={(event) => setEmail(event.target.value)}
                        onKeyDown={(event) => {
                          if (event.key === "Enter" && canSubmit) void submit()
                        }}
                        autoCapitalize="none"
                        autoComplete="email"
                        spellCheck={false}
                      />
                    </InputGroup>
                  </Field>
                  <Field data-invalid={mode === "register" && password.length > 0 &&
                    !passwordReady ? true : undefined}>
                    <FieldLabel htmlFor="login-password">Password</FieldLabel>
                    <InputGroup>
                      <InputGroupInput
                        id="login-password"
                        type="password"
                        placeholder={mode === "register" ? "At least 8 characters" : "Password"}
                        value={password}
                        onChange={(event) => setPassword(event.target.value)}
                        onKeyDown={(event) => {
                          if (event.key === "Enter" && canSubmit) void submit()
                        }}
                        autoComplete={mode === "signin" ? "current-password" : "new-password"}
                      />
                    </InputGroup>
                    {mode === "register" ? (
                      <FieldDescription>
                        Students create their own accounts; teachers are enrolled by the admin.
                      </FieldDescription>
                    ) : null}
                  </Field>
                  <Button disabled={!canSubmit} onClick={() => void submit()}>
                    {pending ? (
                      <>
                        <Spinner data-icon="inline-start" />
                        Working…
                      </>
                    ) : mode === "signin" ? (
                      <>
                        <LogInIcon data-icon="inline-start" />
                        Sign in
                      </>
                    ) : (
                      <>
                        <UserPlusIcon data-icon="inline-start" />
                        Create account
                      </>
                    )}
                  </Button>
                </FieldGroup>
              </TabsContent>
            </Tabs>
          </CardContent>
        </Card>
      </div>
    </AppShell>
  )
}
