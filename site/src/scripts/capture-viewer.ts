// Opens EvidenceFigure captures in the CaptureViewer dialog instead of the
// raw file. Without JS (or without <dialog>) the links still open the file.

export function bootCaptureViewer(): void {
  const dialog = document.querySelector<HTMLDialogElement>("[data-capture-viewer]");
  if (!dialog || typeof dialog.showModal !== "function") return;
  const img = document.createElement("img");
  dialog.querySelector("[data-capture-frame]")!.append(img);
  const scroller = dialog.querySelector<HTMLElement>("[data-capture-scroll]")!;
  let trigger: HTMLElement | null = null;

  for (const link of document.querySelectorAll<HTMLAnchorElement>("a[data-capture-open]")) {
    const thumb = link.querySelector("img");
    if (!thumb) continue;
    link.setAttribute("aria-haspopup", "dialog");
    link.addEventListener("click", (e) => {
      // Modified clicks keep their browser meaning (new tab, download).
      if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
      e.preventDefault();
      trigger = link;
      img.src = link.href;
      img.alt = thumb.alt;
      // The thumbnail's width/height attributes are the file's natural size.
      img.setAttribute("width", thumb.getAttribute("width") ?? "");
      img.setAttribute("height", thumb.getAttribute("height") ?? "");
      dialog.showModal();
      scroller.scrollTo(0, 0);
    });
  }

  dialog.querySelector("[data-capture-close]")!.addEventListener("click", () => dialog.close());
  // Esc closes natively; every close hands focus back to the opening image.
  dialog.addEventListener("close", () => {
    trigger?.focus();
    trigger = null;
  });
}
