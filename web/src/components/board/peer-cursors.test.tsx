import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import type { PeerInfo } from "@/lib/types"
import { PeerCursors } from "./peer-cursors"

function peer(id: string, name: string, cursor: [number, number] | null): PeerInfo {
  return {
    id,
    name,
    role: "student",
    cursor: cursor ? { x: cursor[0], y: cursor[1], tool: "pointer" } : null,
  }
}

describe("PeerCursors", () => {
  it("renders remote cursors at their percentage positions with names", () => {
    render(
      <PeerCursors
        peers={[peer("p1", "Bob", [12.5, 40])]}
        selfId="me"
      />,
    )
    const label = screen.getByText("Bob")
    const wrapper = label.closest("div[style]") as HTMLElement
    expect(wrapper.style.left).toBe("12.5%")
    expect(wrapper.style.top).toBe("40%")
    // Screen-reader hidden container.
    expect(wrapper.parentElement?.getAttribute("aria-hidden")).toBe("true")
  })

  it("hides your own cursor and peers without positions", () => {
    const { container } = render(
      <PeerCursors
        peers={[
          peer("me", "You", [50, 50]),
          peer("p2", "Idle", null),
        ]}
        selfId="me"
      />,
    )
    expect(container.childElementCount).toBe(0)
  })

  it("renders nothing when nobody is present", () => {
    const { container } = render(<PeerCursors peers={[]} selfId="me" />)
    expect(container.childElementCount).toBe(0)
  })
})
