import { useEffect, useRef, useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import {
  EyeIcon,
  EyeOffIcon,
  LogInIcon,
  PenToolIcon,
  UserPlusIcon,
} from "lucide-react"

import { api, ApiError } from "@/lib/api"
import { clearAuth, setAuth, validAuthUser } from "@/lib/auth"
import { safeRedirect } from "@/lib/navigation"
import {
  isPasswordLengthValid,
  passwordLengthMessage,
} from "@/lib/password-policy"
import { AppShell } from "@/components/layout/app-shell"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import {
  InputGroup,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

type LoginSearch = { redirect?: string; token?: string }

export const Route = createFileRoute("/login")({
  validateSearch: (search: Record<string, unknown>): LoginSearch => ({
    redirect:
      typeof search.redirect === "string" && search.redirect.startsWith("/")
        ? search.redirect
        : undefined,
    token: typeof search.token === "string" ? search.token : undefined,
  }),
  component: LoginPage,
  head: () => ({ meta: [{ title: "Sign in · Continuum" }] }),
})

export function validateLoginInput(email: string, password: string): boolean {
  if (!email.trim() || !password) {
    toast.error("Email and password are required")
    return false
  }
  return true
}

export function LoginPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { redirect: redirectTo, token: resetToken } = Route.useSearch()
  const [mode, setMode] = useState("signin")
  const [showForgot, setShowForgot] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [resetPassword, setResetPassword] = useState("")
  const [showResetPassword, setShowResetPassword] = useState(false)
  const [name, setName] = useState("")
  const [pending, setPending] = useState(false)
  const requestLock = useRef(false)
  const [recoveryMessage, setRecoveryMessage] = useState("")

  useEffect(() => {
    setPassword("")
    setShowPassword(false)
  }, [mode])

  function finish(tokens: Awaited<ReturnType<typeof api.login>>) {
    if (
      !validAuthUser(tokens.user) ||
      !tokens.access_token ||
      !tokens.refresh_token
    )
      throw new Error("The server returned an invalid sign-in response")
    setAuth({
      user: tokens.user,
      accessToken: tokens.access_token,
      refreshToken: tokens.refresh_token,
    })
    queryClient.clear()
    toast.success(
      mode === "signin"
        ? `Welcome back, ${tokens.user.name}`
        : "Account created"
    )
    void navigate({ to: safeRedirect(redirectTo) })
  }

  async function submit() {
    if (requestLock.current) return
    /* c8 ignore next */
    if (!validateLoginInput(email, password)) return
    if (mode === "register" && !isPasswordLengthValid(password)) {
      toast.error(passwordLengthMessage(password) ?? "Choose a valid password")
      return
    }
    requestLock.current = true
    setPending(true)
    try {
      const tokens =
        mode === "signin"
          ? await api.login({ email: email.trim(), password })
          : await api.register({
              email: email.trim(),
              password,
              name: name.trim(),
            })
      finish(tokens)
    } catch (error) {
      toast.error(
        error instanceof ApiError ? error.message : "Authentication failed"
      )
    } finally {
      requestLock.current = false
      setPending(false)
    }
  }

  async function requestResetLink() {
    if (requestLock.current) return
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())) {
      toast.error("Enter a valid email address")
      return
    }
    requestLock.current = true
    setPending(true)
    setRecoveryMessage("")
    try {
      const response = (await api.forgotPassword(email.trim())) as {
        message?: string
        delivery?: string
      }
      const delivery = response.delivery
      setRecoveryMessage(
        delivery && delivery !== "sent"
          ? "Password reset email is unavailable right now. Please try again later."
          : "If an account uses this email, a password reset link will be sent when email delivery is available."
      )
    } catch (error) {
      const status = error instanceof ApiError ? error.status : undefined
      setRecoveryMessage(
        status === 503
          ? "Password reset email is unavailable right now. Please try again later."
          : "We could not process that request right now. Please try again later."
      )
    } finally {
      requestLock.current = false
      setPending(false)
    }
  }

  async function completePasswordReset() {
    if (requestLock.current) return
    if (!resetToken || !isPasswordLengthValid(resetPassword)) {
      toast.error(
        !resetToken
          ? "Use a reset link to continue"
          : (passwordLengthMessage(resetPassword) ?? "Choose a valid password")
      )
      return
    }
    requestLock.current = true
    setPending(true)
    try {
      await api.resetPassword({ token: resetToken, password: resetPassword })
      clearAuth()
      queryClient.clear()
      toast.success("Password reset. You can sign in now.")
      setResetPassword("")
      void navigate({ to: "/login", replace: true })
    } catch (error) {
      toast.error(
        error instanceof ApiError
          ? error.message
          : "Could not reset the password"
      )
    } finally {
      requestLock.current = false
      setPending(false)
    }
  }

  const emailReady = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())
  const passwordReady = isPasswordLengthValid(password)
  const canSubmit =
    emailReady &&
    (mode === "signin" || name.trim().length > 0) &&
    (mode === "signin" || passwordReady) &&
    !pending

  return (
    <AppShell
      title="Sign in"
      description="Your account powers boards and quizzes"
    >
      <div className="mx-auto w-full max-w-md pt-6">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <PenToolIcon data-icon="inline-start" />
              Continuum account
            </CardTitle>
            <CardDescription>
              Students sign in to take quizzes; teachers sign in to run the
              class.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {resetToken ? (
              <FieldGroup>
                <p className="text-sm text-muted-foreground">
                  Choose a new password for your account.
                </p>
                <Field
                  data-invalid={
                    resetPassword.length > 0 &&
                    !isPasswordLengthValid(resetPassword)
                      ? true
                      : undefined
                  }
                >
                  <FieldLabel htmlFor="reset-password">New password</FieldLabel>
                  <InputGroup>
                    <InputGroupInput
                      id="reset-password"
                      autoCapitalize="none"
                      autoCorrect="off"
                      spellCheck={false}
                      aria-invalid={
                        resetPassword.length > 0 &&
                        !isPasswordLengthValid(resetPassword)
                      }
                      aria-describedby="reset-password-help"
                      type={showResetPassword ? "text" : "password"}
                      value={resetPassword}
                      onChange={(event) => setResetPassword(event.target.value)}
                      autoComplete="new-password"
                    />
                    <InputGroupButton
                      type="button"
                      aria-pressed={showResetPassword}
                      aria-label={
                        showResetPassword ? "Hide password" : "Show password"
                      }
                      onClick={() =>
                        setShowResetPassword((visible) => !visible)
                      }
                    >
                      {showResetPassword ? <EyeOffIcon /> : <EyeIcon />}
                    </InputGroupButton>
                  </InputGroup>
                  <FieldDescription id="reset-password-help">
                    {passwordLengthMessage(resetPassword) ??
                      "Use at least 8 characters."}
                  </FieldDescription>
                </Field>
                <Button
                  disabled={!isPasswordLengthValid(resetPassword) || pending}
                  onClick={() => void completePasswordReset()}
                >
                  {pending ? (
                    <>
                      <Spinner data-icon="inline-start" />
                      Working…
                    </>
                  ) : (
                    "Reset password"
                  )}
                </Button>
              </FieldGroup>
            ) : showForgot ? (
              <FieldGroup>
                <p className="text-sm text-muted-foreground">
                  Enter your email. We’ll show the same response whether or not
                  it has an account.
                </p>
                <Field>
                  <FieldLabel htmlFor="forgot-email">Email</FieldLabel>
                  <InputGroup>
                    <InputGroupInput
                      id="forgot-email"
                      autoCapitalize="none"
                      autoCorrect="off"
                      spellCheck={false}
                      type="email"
                      value={email}
                      onChange={(event) => setEmail(event.target.value)}
                      autoComplete="email"
                    />
                  </InputGroup>
                </Field>
                {recoveryMessage ? (
                  <p role="status" className="text-sm text-muted-foreground">
                    {recoveryMessage}
                  </p>
                ) : null}
                <Button
                  disabled={!emailReady || pending}
                  onClick={() => void requestResetLink()}
                >
                  {pending ? (
                    <>
                      <Spinner data-icon="inline-start" />
                      Working…
                    </>
                  ) : (
                    "Send reset link"
                  )}
                </Button>
                <Button
                  variant="ghost"
                  onClick={() => {
                    setShowForgot(false)
                    setRecoveryMessage("")
                  }}
                >
                  Back to sign in
                </Button>
              </FieldGroup>
            ) : (
              <Tabs
                value={mode}
                onValueChange={(value) =>
                  setMode(value === "register" ? "register" : "signin")
                }
              >
                <TabsList className="w-full">
                  <TabsTrigger
                    value="signin"
                    disabled={pending}
                    className="flex-1"
                  >
                    Sign in
                  </TabsTrigger>
                  <TabsTrigger
                    value="register"
                    disabled={pending}
                    className="flex-1"
                  >
                    Create account
                  </TabsTrigger>
                </TabsList>
                <TabsContent value={mode}>
                  <FieldGroup className="pt-2">
                    {mode === "register" ? (
                      <Field
                        data-invalid={
                          name.length > 0 && !name.trim() ? true : undefined
                        }
                      >
                        <FieldLabel htmlFor="login-name">
                          Display name
                        </FieldLabel>
                        <InputGroup>
                          <InputGroupInput
                            id="login-name"
                            aria-invalid={name.length > 0 && !name.trim()}
                            placeholder="Ada Lovelace"
                            value={name}
                            onChange={(event) => setName(event.target.value)}
                            autoComplete="name"
                          />
                        </InputGroup>
                      </Field>
                    ) : null}
                    <Field
                      data-invalid={
                        email.length > 0 && !emailReady ? true : undefined
                      }
                    >
                      <FieldLabel htmlFor="login-email">Email</FieldLabel>
                      <InputGroup>
                        <InputGroupInput
                          id="login-email"
                          aria-invalid={email.length > 0 && !emailReady}
                          type="email"
                          placeholder="you@example.com"
                          value={email}
                          onChange={(event) => setEmail(event.target.value)}
                          onKeyDown={(event) => {
                            if (
                              event.key === "Enter" &&
                              !event.nativeEvent.isComposing &&
                              canSubmit
                            )
                              void submit()
                          }}
                          autoCapitalize="none"
                          autoComplete="email"
                          spellCheck={false}
                        />
                      </InputGroup>
                    </Field>
                    <Field
                      data-invalid={
                        mode === "register" &&
                        password.length > 0 &&
                        !passwordReady
                          ? true
                          : undefined
                      }
                    >
                      <FieldLabel htmlFor="login-password">Password</FieldLabel>
                      <InputGroup>
                        <InputGroupInput
                          id="login-password"
                          autoCapitalize="none"
                          autoCorrect="off"
                          spellCheck={false}
                          aria-invalid={
                            mode === "register" &&
                            password.length > 0 &&
                            !passwordReady
                          }
                          aria-describedby={
                            mode === "register"
                              ? "login-password-help"
                              : undefined
                          }
                          type={showPassword ? "text" : "password"}
                          placeholder={
                            mode === "register"
                              ? "At least 8 characters"
                              : "Password"
                          }
                          value={password}
                          onChange={(event) => setPassword(event.target.value)}
                          onKeyDown={(event) => {
                            if (
                              event.key === "Enter" &&
                              !event.nativeEvent.isComposing &&
                              canSubmit
                            )
                              void submit()
                          }}
                          autoComplete={
                            mode === "signin"
                              ? "current-password"
                              : "new-password"
                          }
                        />
                        <InputGroupButton
                          type="button"
                          aria-pressed={showPassword}
                          aria-label={
                            showPassword ? "Hide password" : "Show password"
                          }
                          onClick={() => setShowPassword((visible) => !visible)}
                        >
                          {showPassword ? <EyeOffIcon /> : <EyeIcon />}
                        </InputGroupButton>
                      </InputGroup>
                      {mode === "register" ? (
                        <FieldDescription id="login-password-help">
                          {passwordLengthMessage(password) ??
                            "Use at least 8 characters."}{" "}
                          Students create their own accounts; teachers are
                          enrolled by the admin.
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
                    {mode === "signin" ? (
                      <Button
                        variant="link"
                        className="h-auto self-start px-0"
                        onClick={() => setShowForgot(true)}
                      >
                        Forgot password?
                      </Button>
                    ) : null}
                  </FieldGroup>
                </TabsContent>
              </Tabs>
            )}
          </CardContent>
        </Card>
      </div>
    </AppShell>
  )
}
