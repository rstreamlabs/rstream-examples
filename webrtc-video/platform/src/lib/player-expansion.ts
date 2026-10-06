// Expanding changes layout only: the video element and its MediaStream stay
// mounted. No browser fullscreen permission or native video player is involved.
export function containExpandedPlayer(
  player: HTMLElement,
  exit: HTMLButtonElement,
  onEscape: () => void,
  scroll: { x: number; y: number },
) {
  const { x: scrollX, y: scrollY } = scroll
  const restoreStyles: (() => void)[] = []
  function style(element: HTMLElement, name: string, value: string) {
    const previous = element.style.getPropertyValue(name)
    const priority = element.style.getPropertyPriority(name)
    element.style.setProperty(name, value)
    restoreStyles.push(() => {
      if (previous) element.style.setProperty(name, previous, priority)
      else element.style.removeProperty(name)
    })
  }
  // Fixed body also prevents background touch scrolling on iOS. Save the
  // original offsets and individual styles instead of replacing cssText.
  style(document.documentElement, "overflow", "hidden")
  style(document.body, "position", "fixed")
  style(document.body, "top", `${-scrollY}px`)
  style(document.body, "left", `${-scrollX}px`)
  style(document.body, "width", "100%")
  style(document.body, "overflow", "hidden")
  const inactive: { element: HTMLElement; inert: boolean }[] = []
  for (
    let branch: HTMLElement | null = player;
    branch?.parentElement;
    branch = branch.parentElement
  ) {
    for (const sibling of branch.parentElement.children) {
      if (sibling === branch || !(sibling instanceof HTMLElement)) continue
      inactive.push({ element: sibling, inert: sibling.inert })
      sibling.inert = true
    }
    if (branch.parentElement === document.body) break
  }
  const focusExit = () => exit.focus({ preventScroll: true })
  const onFocus = (event: FocusEvent) => {
    if (event.target instanceof Node && !player.contains(event.target))
      focusExit()
  }
  const onKey = (event: KeyboardEvent) => {
    if (event.defaultPrevented) return
    if (event.key === "Escape") {
      event.preventDefault()
      onEscape()
    } else if (event.key === "Tab") {
      const controls = [
        ...player.querySelectorAll<HTMLElement>(
          "button, select, input, textarea, a[href], [tabindex]",
        ),
      ].filter(
        (element) =>
          element.tabIndex >= 0 &&
          !element.matches(":disabled, [inert], [inert] *") &&
          element.getClientRects().length > 0,
      )
      const next = event.shiftKey ? controls.at(-1) : controls[0]
      const boundary = event.shiftKey ? controls[0] : controls.at(-1)
      if (!controls.length || document.activeElement === boundary) {
        event.preventDefault()
        ;(next ?? exit).focus({ preventScroll: true })
      }
    }
  }
  document.addEventListener("keydown", onKey)
  document.addEventListener("focusin", onFocus)
  focusExit()
  return () => {
    document.removeEventListener("keydown", onKey)
    document.removeEventListener("focusin", onFocus)
    for (const { element, inert } of inactive) element.inert = inert
    for (const restore of restoreStyles.reverse()) restore()
    // React runs layout-effect cleanup before updating/removing the expanded
    // DOM. Restore offsets after that commit, when inline content contributes
    // to page height again; otherwise the browser clamps the offset to zero.
    queueMicrotask(() => {
      window.scrollTo({ left: scrollX, top: scrollY, behavior: "instant" })
      // On unmount there may no longer be a control to return focus to.
      if (exit.isConnected) focusExit()
    })
  }
}
