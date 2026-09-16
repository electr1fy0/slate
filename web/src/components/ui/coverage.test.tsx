import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { Alert, AlertAction, AlertDescription, AlertTitle } from "./alert"
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogMedia, AlertDialogTitle, AlertDialogTrigger } from "./alert-dialog"
import { Avatar, AvatarBadge, AvatarFallback, AvatarGroup, AvatarGroupCount, AvatarImage } from "./avatar"
import { Badge } from "./badge"
import { Breadcrumb, BreadcrumbEllipsis, BreadcrumbItem, BreadcrumbLink, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator } from "./breadcrumb"
import { Button } from "./button"
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "./card"
import { Checkbox } from "./checkbox"
import { Command, CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandSeparator, CommandShortcut } from "./command"
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "./dialog"
import { DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuPortal, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSeparator, DropdownMenuShortcut, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger } from "./dropdown-menu"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "./empty"
import { Field, FieldContent, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSeparator, FieldSet, FieldTitle } from "./field"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput, InputGroupText, InputGroupTextarea } from "./input-group"
import { Input } from "./input"
import { Kbd, KbdGroup } from "./kbd"
import { Label } from "./label"
import { Popover, PopoverContent, PopoverDescription, PopoverHeader, PopoverTitle, PopoverTrigger } from "./popover"
import { Progress, ProgressIndicator, ProgressLabel, ProgressTrack, ProgressValue } from "./progress"
import { RadioGroup, RadioGroupItem } from "./radio-group"
import { ScrollArea, ScrollBar } from "./scroll-area"
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectSeparator, SelectTrigger, SelectValue } from "./select"
import { Separator } from "./separator"
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle, SheetTrigger } from "./sheet"
import { Skeleton } from "./skeleton"
import { Slider } from "./slider"
import { Toaster } from "./sonner"
import { Spinner } from "./spinner"
import { Switch } from "./switch"
import { Table, TableBody, TableCaption, TableCell, TableFooter, TableHead, TableHeader, TableRow } from "./table"
import { Textarea } from "./textarea"
import { ToggleGroup, ToggleGroupItem } from "./toggle-group"
import { Toggle } from "./toggle"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./tooltip"

describe("presentational UI primitives", () => {
  it("renders the simple primitives, variants, and composition slots", () => {
    render(
      <div>
        <Alert variant="default"><AlertTitle>Notice</AlertTitle><AlertDescription>Details</AlertDescription><AlertAction>Action</AlertAction></Alert>
        <Alert variant="destructive" className="custom"><AlertTitle>Danger</AlertTitle><AlertDescription>Stop</AlertDescription></Alert>
        <Avatar size="sm"><AvatarImage src="/avatar.png" alt="Avatar" /><AvatarFallback>AB</AvatarFallback><AvatarBadge>1</AvatarBadge></Avatar>
        <AvatarGroup><Avatar size="lg"><AvatarFallback>CD</AvatarFallback></Avatar><AvatarGroupCount>+3</AvatarGroupCount></AvatarGroup>
        <div><Badge>default</Badge><Badge variant="secondary">secondary</Badge><Badge variant="destructive">destructive</Badge><Badge variant="outline">outline</Badge><Badge variant="ghost">ghost</Badge><Badge variant="link">link</Badge></div>
        <Breadcrumb><BreadcrumbList><BreadcrumbItem><BreadcrumbLink href="/">Home</BreadcrumbLink></BreadcrumbItem><BreadcrumbSeparator /><BreadcrumbItem><BreadcrumbPage>Current</BreadcrumbPage></BreadcrumbItem><BreadcrumbEllipsis /></BreadcrumbList></Breadcrumb>
        <div>{(["default", "outline", "secondary", "ghost", "destructive", "link"] as const).map((variant) => <Button key={variant} variant={variant}>{variant}</Button>)}</div>
        <div>{(["default", "xs", "sm", "lg", "icon", "icon-xs", "icon-sm", "icon-lg"] as const).map((size) => <Button key={size} size={size} aria-label={size}>{size}</Button>)}</div>
        <Card size="sm"><CardHeader><CardTitle>Title</CardTitle><CardDescription>Description</CardDescription><CardAction>More</CardAction></CardHeader><CardContent>Body</CardContent><CardFooter>Footer</CardFooter></Card>
        <Checkbox aria-label="check" defaultChecked />
        <Empty><EmptyHeader><EmptyMedia variant="default">Icon</EmptyMedia><EmptyTitle>Empty</EmptyTitle></EmptyHeader><EmptyDescription>Nothing here</EmptyDescription><EmptyContent>Try again</EmptyContent></Empty>
        <FieldSet><FieldLegend variant="legend">Legend</FieldLegend><FieldLegend variant="label">Label</FieldLegend><FieldGroup><Field orientation="vertical"><FieldLabel htmlFor="name">Name</FieldLabel><FieldContent><Input id="name" /><FieldDescription>Help</FieldDescription></FieldContent><FieldTitle>Title</FieldTitle><FieldError errors={[{ message: "Bad" }, { message: "Bad" }, { message: "Worse" }]} /><FieldError errors={[{ message: "Only" }]} /></Field><Field orientation="horizontal" /><Field orientation="responsive" /><FieldSeparator>Or</FieldSeparator><FieldError>Inline error</FieldError><FieldError /></FieldGroup></FieldSet>
        <Label htmlFor="text">Text</Label><Input id="text" placeholder="Input" /><Textarea placeholder="Text area" />
        <KbdGroup><Kbd>⌘</Kbd><Kbd> K </Kbd></KbdGroup>
        <Separator orientation="vertical" /><Skeleton /><Spinner /><Switch size="sm" defaultChecked />
        <Progress value={50}><ProgressLabel>Progress</ProgressLabel><ProgressValue /><ProgressTrack><ProgressIndicator /></ProgressTrack></Progress>
        <RadioGroup defaultValue="one" aria-label="radio"><RadioGroupItem value="one" /><RadioGroupItem value="two" /></RadioGroup>
        <Table><TableCaption>Caption</TableCaption><TableHeader><TableRow><TableHead>Head</TableHead></TableRow></TableHeader><TableBody><TableRow><TableCell>Cell</TableCell></TableRow></TableBody><TableFooter><TableRow><TableCell>Total</TableCell></TableRow></TableFooter></Table>
        <Toggle variant="outline" size="lg">Toggle</Toggle><ToggleGroup variant="outline" size="sm" spacing={0}><ToggleGroupItem value="a">A</ToggleGroupItem><ToggleGroupItem value="b">B</ToggleGroupItem></ToggleGroup><ToggleGroup><ToggleGroupItem value="fallback" variant="outline" size="lg">Fallback</ToggleGroupItem></ToggleGroup>
        <InputGroup><InputGroupAddon align="inline-start">$</InputGroupAddon><InputGroupInput aria-label="group input" /><InputGroupAddon align="inline-end"><InputGroupButton size="icon-sm" aria-label="go">Go</InputGroupButton></InputGroupAddon><InputGroupText>Hint</InputGroupText><InputGroupTextarea /></InputGroup>
        <Slider defaultValue={[25]} aria-label="volume" /><Slider value={[50]} aria-label="fixed volume" /><Slider aria-label="fallback volume" />
        <ScrollArea><div>Scrollable</div><ScrollBar orientation="horizontal" /></ScrollArea>
      </div>,
    )
    expect(screen.getByText("Notice")).toBeTruthy()
    expect(screen.getByText("Body")).toBeTruthy()
    expect(screen.getByRole("checkbox").getAttribute("data-checked")).toBe("")
    expect(screen.getByText("Inline error")).toBeTruthy()
    expect(screen.getByText("Only")).toBeTruthy()
    fireEvent.click(screen.getByText("$"))
    expect(document.activeElement).toBe(screen.getByRole("textbox", { name: "group input" }))
  })

  it("opens dialog, alert dialog, popover, sheet, tooltip, select, and dropdown primitives", () => {
    const onSelect = vi.fn()
    render(
      <div>
        <Dialog open><DialogTrigger>Open dialog</DialogTrigger><DialogContent showCloseButton><DialogHeader><DialogTitle>Dialog</DialogTitle><DialogDescription>Dialog details</DialogDescription></DialogHeader><DialogFooter showCloseButton><DialogClose>Cancel</DialogClose></DialogFooter></DialogContent></Dialog>
        <AlertDialog open><AlertDialogTrigger>Delete</AlertDialogTrigger><AlertDialogContent size="sm"><AlertDialogHeader><AlertDialogMedia>!</AlertDialogMedia><AlertDialogTitle>Confirm</AlertDialogTitle><AlertDialogDescription>Are you sure?</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel size="sm">Cancel</AlertDialogCancel><AlertDialogAction>Delete</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>
        <Popover open><PopoverTrigger>Popover</PopoverTrigger><PopoverContent side="top"><PopoverHeader><PopoverTitle>Popover title</PopoverTitle><PopoverDescription>Popover details</PopoverDescription></PopoverHeader></PopoverContent></Popover>
        <Sheet open><SheetTrigger>Sheet</SheetTrigger><SheetContent side="left"><SheetHeader><SheetTitle>Sheet title</SheetTitle><SheetDescription>Sheet details</SheetDescription></SheetHeader><SheetFooter><SheetClose>Close sheet</SheetClose></SheetFooter></SheetContent></Sheet>
        <TooltipProvider delay={10}><Tooltip open><TooltipTrigger>Tip</TooltipTrigger><TooltipContent side="right">Tooltip content</TooltipContent></Tooltip></TooltipProvider>
        <Select open defaultValue="one" onValueChange={onSelect}><SelectTrigger size="sm"><SelectValue placeholder="Choose" /></SelectTrigger><SelectContent alignItemWithTrigger={false}><SelectGroup><SelectLabel>Options</SelectLabel><SelectItem value="one">One</SelectItem><SelectSeparator /><SelectItem value="two">Two</SelectItem></SelectGroup></SelectContent></Select>
        <DropdownMenu open><DropdownMenuTrigger>Menu</DropdownMenuTrigger><DropdownMenuPortal><span>Portal content</span></DropdownMenuPortal><DropdownMenuContent side="top"><DropdownMenuGroup><DropdownMenuLabel inset>Menu label</DropdownMenuLabel><DropdownMenuItem inset>Item<DropdownMenuShortcut>⌘I</DropdownMenuShortcut></DropdownMenuItem><DropdownMenuItem variant="destructive">Remove</DropdownMenuItem><DropdownMenuCheckboxItem checked inset>Checked</DropdownMenuCheckboxItem><DropdownMenuRadioGroup value="one"><DropdownMenuRadioItem value="one" inset>Radio</DropdownMenuRadioItem></DropdownMenuRadioGroup><DropdownMenuSeparator /><DropdownMenuSub open><DropdownMenuSubTrigger inset>More</DropdownMenuSubTrigger><DropdownMenuSubContent><DropdownMenuItem>Nested</DropdownMenuItem></DropdownMenuSubContent></DropdownMenuSub></DropdownMenuGroup></DropdownMenuContent></DropdownMenu>
        <Command><CommandInput placeholder="Search" /><CommandList><CommandEmpty>No results</CommandEmpty><CommandGroup heading="Commands"><CommandItem value="one">One<CommandShortcut>⌘1</CommandShortcut></CommandItem></CommandGroup><CommandSeparator /></CommandList></Command>
        <CommandDialog open title="Commands" description="Command list"><Command><CommandInput placeholder="Dialog search" /><CommandList><CommandItem value="dialog">Dialog command</CommandItem></CommandList></Command></CommandDialog>
        <Toaster />
      </div>,
    )
    expect(screen.getByText("Dialog")).toBeTruthy()
    expect(screen.getByText("Confirm")).toBeTruthy()
    expect(screen.getByText("Popover title")).toBeTruthy()
    expect(screen.getByText("Sheet title")).toBeTruthy()
    expect(screen.getByText("Tooltip content")).toBeTruthy()
    expect(screen.getByText("Nested")).toBeTruthy()
    expect(screen.getByText("Portal content")).toBeTruthy()
    expect(screen.getByText("Dialog command")).toBeTruthy()
    expect(screen.getByPlaceholderText("Search")).toBeTruthy()
    expect(screen.getByText("Two")).toBeTruthy()
  })
})
