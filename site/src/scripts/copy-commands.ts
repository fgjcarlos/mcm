// Copy buttons on CommandStep blocks. They render hidden and only appear
// here, so without JS there is no dead button.

export function bootCopyCommands(): void {
  for (const button of document.querySelectorAll<HTMLButtonElement>("[data-copy]")) {
    const step = button.closest("[data-command-step]");
    const code = step?.querySelector("[data-copy-source]");
    const status = step?.querySelector<HTMLElement>("[data-copy-status]");
    if (!code || !status) continue;
    let timer = 0;
    button.hidden = false;
    button.addEventListener("click", async () => {
      clearTimeout(timer);
      try {
        await navigator.clipboard.writeText(code.textContent ?? "");
        status.textContent = "Copied";
        // Cleared so a second copy is announced again.
        timer = window.setTimeout(() => (status.textContent = ""), 3000);
      } catch {
        status.textContent = "Copy unavailable: select the command and copy it manually";
      }
    });
  }
}
