/** Moves focus to the first field marked invalid inside a form (PRD-005 UI-NFR-2). */
export function focusFirstInvalid(form: HTMLFormElement | null): void {
  requestAnimationFrame(() => {
    form?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
  });
}
