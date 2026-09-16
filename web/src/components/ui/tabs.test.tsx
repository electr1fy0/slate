import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

describe("ui/tabs smoke", () => {
  it("clicking a trigger switches controlled panels", () => {
    const onValueChange = vi.fn()
    const { rerender } = render(
      <Tabs value="a" onValueChange={onValueChange}>
        <TabsList>
          <TabsTrigger value="a">A</TabsTrigger>
          <TabsTrigger value="b">B</TabsTrigger>
        </TabsList>
        <TabsContent value="a">panel-a</TabsContent>
        <TabsContent value="b">panel-b</TabsContent>
      </Tabs>,
    )
    expect(screen.getByText("panel-a")).toBeTruthy()
    const tabB = screen.getByRole("tab", { name: "B" })
    fireEvent.pointerDown(tabB, { button: 0 })
    fireEvent.focus(tabB)
    fireEvent.click(tabB)
    expect(onValueChange).toHaveBeenCalledWith(
      "b",
      expect.objectContaining({ reason: expect.anything() }),
    )
    rerender(
      <Tabs value="b" onValueChange={onValueChange}>
        <TabsList>
          <TabsTrigger value="a">A</TabsTrigger>
          <TabsTrigger value="b">B</TabsTrigger>
        </TabsList>
        <TabsContent value="a">panel-a</TabsContent>
        <TabsContent value="b">panel-b</TabsContent>
      </Tabs>,
    )
    expect(screen.getByText("panel-a").getAttribute("inert")).toBe("")
    expect(screen.getByText("panel-b")).toBeTruthy()
  })
})
