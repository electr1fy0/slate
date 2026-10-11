import { PlusIcon } from "lucide-react"

import {
  selectClassroom,
  useClassrooms,
  useSelectedClassroom,
} from "@/lib/classrooms"
import type { Classroom } from "@/lib/classrooms"
import { newRoomCode } from "@/lib/room-code"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

export {
  ClassroomDashboard,
  classInviteUrl,
  filterRoster,
} from "./classroom-dashboard-real"

export function classroomSelectorValue(
  selected: Classroom | null
): string | null {
  return selected?.id ?? null
}

export function selectClassroomFromValue(value: unknown): void {
  selectClassroom(typeof value === "string" ? value : null)
}

export function withSelectedClassroom<T>(
  selected: Classroom | null,
  action: (classroom: Classroom) => T
): T | undefined {
  return selected ? action(selected) : undefined
}

export function handleClassroomJoinResult(
  joined: Classroom | null,
  onFailure: () => void,
  onSuccess: (classroom: Classroom) => void
): void {
  if (joined) onSuccess(joined)
  else onFailure()
}

export function ClassroomSelector({
  onCreate,
  className,
}: {
  onCreate?: () => void
  className?: string
}) {
  const classrooms = useClassrooms()
  const selected = useSelectedClassroom()

  if (classrooms.length === 0) {
    return (
      <Button
        variant="outline"
        size="sm"
        onClick={onCreate}
        className={className}
      >
        <PlusIcon data-icon="inline-start" />
        Set up a class
      </Button>
    )
  }

  return (
    <Select
      value={classroomSelectorValue(selected)}
      onValueChange={selectClassroomFromValue}
    >
      <SelectTrigger
        className={className ?? "w-full sm:w-56"}
        aria-label="Select a class"
      >
        <SelectValue placeholder="Select a class" />
      </SelectTrigger>
      <SelectContent>
        {classrooms.map((classroom) => (
          <SelectItem key={classroom.id} value={classroom.id}>
            {classroom.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

export function classBoardRoom(
  classroom: Pick<Classroom, "id" | "active_room_id">
): string {
  return (
    classroom.active_room_id ??
    `class-${classroom.id.replace(/[^a-z0-9]/gi, "").slice(-8)}-${newRoomCode().slice(0, 4)}`
  )
}
