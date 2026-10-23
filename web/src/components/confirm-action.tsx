import { useRef, useState } from "react"
import type { ReactElement } from "react"
import {
  AlertDialog,
  AlertDialogTrigger,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogAction,
} from "@/components/ui/alert-dialog"

/** Destructive actions run only after confirmation. Failures keep the dialog open. */
export function ConfirmAction({
  children,
  title,
  description,
  onConfirm,
  confirmLabel = "Confirm",
}: {
  children: ReactElement
  title: string
  description: string
  onConfirm: () => Promise<unknown> | unknown
  confirmLabel?: string
}) {
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState(false)
  const lock = useRef(false)
  const [error, setError] = useState<string | null>(null)
  async function confirm() {
    if (lock.current) return
    lock.current = true
    setPending(true)
    setError(null)
    try {
      await onConfirm()
      setOpen(false)
    } catch (cause) {
      setError(
        cause instanceof Error ? cause.message : "The action failed. Try again."
      )
    } finally {
      lock.current = false
      setPending(false)
    }
  }
  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!lock.current) {
          setOpen(next)
          setError(null)
        }
      }}
    >
      <AlertDialogTrigger render={children} />
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        {error ? (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={pending}
            onClick={() => void confirm()}
          >
            {pending ? "Working…" : confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
