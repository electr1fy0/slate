import { useEffect, useRef, useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { EyeIcon, EyeOffIcon, KeyRoundIcon, UserRoundIcon } from "lucide-react"

import { api, ApiError } from "@/lib/api"
import { clearAuth, getAuth, setAuth, useAuth } from "@/lib/auth"
import { requireAuth } from "@/lib/guard"
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

export const Route = createFileRoute("/settings")({
  ssr: false,
  beforeLoad: ({ location }) => requireAuth(location),
  component: AccountSettingsPage,
  head: () => ({ meta: [{ title: "Account settings · Continuum" }] }),
})

export function AccountSettingsPage() {
  const auth = useAuth()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [name, setName] = useState(auth.user?.name ?? "")
  const [currentPassword, setCurrentPassword] = useState("")
  const [newPassword, setNewPassword] = useState("")
  const [showCurrentPassword, setShowCurrentPassword] = useState(false)
  const [showNewPassword, setShowNewPassword] = useState(false)
  const [profilePending, setProfilePending] = useState(false)
  const [passwordPending, setPasswordPending] = useState(false)
  const profileLock = useRef(false)
  const passwordLock = useRef(false)

  useEffect(() => {
    setName(auth.user?.name ?? "")
  }, [auth.user?.name, auth.user?.user_id])

  useEffect(() => {
    setCurrentPassword("")
    setNewPassword("")
    setShowCurrentPassword(false)
    setShowNewPassword(false)
  }, [auth.user?.user_id])

  async function saveProfile() {
    if (profileLock.current) return
    const requestedUserId = auth.user?.user_id
    const cleanName = name.trim()
    if (!requestedUserId || !cleanName) {
      toast.error("Enter a name")
      return
    }
    profileLock.current = true
    setProfilePending(true)
    try {
      const updatedUser = await api.updateProfile({ name: cleanName })
      const currentAuth = getAuth()
      if (currentAuth.user?.user_id !== requestedUserId) return
      setAuth({ ...currentAuth, user: updatedUser })
      setName(updatedUser.name)
      toast.success("Profile updated")
    } catch (error) {
      if (getAuth().user?.user_id === requestedUserId) {
        toast.error(
          error instanceof ApiError
            ? error.message
            : "Could not update your profile"
        )
      }
    } finally {
      profileLock.current = false
      setProfilePending(false)
    }
  }

  async function changePassword() {
    if (passwordLock.current) return
    const requestedUserId = auth.user?.user_id
    if (
      !requestedUserId ||
      !currentPassword ||
      !isPasswordLengthValid(newPassword)
    ) {
      toast.error("Enter your current password and a valid new password")
      return
    }
    passwordLock.current = true
    setPasswordPending(true)
    try {
      await api.changePassword({
        current_password: currentPassword,
        password: newPassword,
      })
      if (getAuth().user?.user_id !== requestedUserId) return
      setCurrentPassword("")
      setNewPassword("")
      clearAuth()
      queryClient.clear()
      toast.success("Password changed. Sign in again with your new password.")
      void navigate({ to: "/login" })
    } catch (error) {
      if (getAuth().user?.user_id === requestedUserId) {
        toast.error(
          error instanceof ApiError
            ? error.message
            : "Could not change your password"
        )
      }
    } finally {
      passwordLock.current = false
      setPasswordPending(false)
    }
  }

  return (
    <AppShell
      title="Account settings"
      description="Manage your profile and sign-in credentials"
    >
      <div className="mx-auto flex w-full max-w-2xl flex-col gap-5 pt-4">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <UserRoundIcon data-icon="inline-start" />
              Profile
            </CardTitle>
            <CardDescription>
              Update the name shown in your classes and boards.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="profile-name">Display name</FieldLabel>
                <InputGroup>
                  <InputGroupInput
                    id="profile-name"
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    autoComplete="name"
                  />
                </InputGroup>
              </Field>
              <Field>
                <FieldLabel htmlFor="profile-email">Email</FieldLabel>
                <InputGroup>
                  <InputGroupInput
                    id="profile-email"
                    value={auth.user?.email ?? ""}
                    readOnly
                  />
                </InputGroup>
                <FieldDescription>Email is used to sign in.</FieldDescription>
              </Field>
              <Button
                disabled={
                  !name.trim() ||
                  name.trim() === auth.user?.name ||
                  profilePending
                }
                onClick={() => void saveProfile()}
              >
                {profilePending ? (
                  <>
                    <Spinner data-icon="inline-start" />
                    Saving…
                  </>
                ) : (
                  "Save profile"
                )}
              </Button>
            </FieldGroup>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <KeyRoundIcon data-icon="inline-start" />
              Change password
            </CardTitle>
            <CardDescription>
              Use at least 8 characters. Very long passwords may be rejected.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="current-password">
                  Current password
                </FieldLabel>
                <InputGroup>
                  <InputGroupInput
                    id="current-password"
                    type={showCurrentPassword ? "text" : "password"}
                    value={currentPassword}
                    onChange={(event) => setCurrentPassword(event.target.value)}
                    autoComplete="current-password"
                  />
                  <InputGroupButton
                    aria-label={
                      showCurrentPassword
                        ? "Hide current password"
                        : "Show current password"
                    }
                    onClick={() =>
                      setShowCurrentPassword((visible) => !visible)
                    }
                  >
                    {showCurrentPassword ? <EyeOffIcon /> : <EyeIcon />}
                  </InputGroupButton>
                </InputGroup>
              </Field>
              <Field
                data-invalid={
                  newPassword.length > 0 && !isPasswordLengthValid(newPassword)
                    ? true
                    : undefined
                }
              >
                <FieldLabel htmlFor="new-password">New password</FieldLabel>
                <InputGroup>
                  <InputGroupInput
                    id="new-password"
                    type={showNewPassword ? "text" : "password"}
                    value={newPassword}
                    onChange={(event) => setNewPassword(event.target.value)}
                    autoComplete="new-password"
                  />
                  <InputGroupButton
                    aria-label={
                      showNewPassword
                        ? "Hide new password"
                        : "Show new password"
                    }
                    onClick={() => setShowNewPassword((visible) => !visible)}
                  >
                    {showNewPassword ? <EyeOffIcon /> : <EyeIcon />}
                  </InputGroupButton>
                </InputGroup>
                <FieldDescription>
                  {passwordLengthMessage(newPassword) ??
                    "Use at least 8 characters."}
                </FieldDescription>
              </Field>
              <Button
                disabled={
                  !currentPassword ||
                  !isPasswordLengthValid(newPassword) ||
                  passwordPending
                }
                onClick={() => void changePassword()}
              >
                {passwordPending ? (
                  <>
                    <Spinner data-icon="inline-start" />
                    Updating…
                  </>
                ) : (
                  "Change password"
                )}
              </Button>
            </FieldGroup>
          </CardContent>
        </Card>
      </div>
    </AppShell>
  )
}
