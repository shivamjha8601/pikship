# Accessibility (a11y)

## Scope

We have three distinct UI surfaces with different accessibility profiles:
1. **Seller dashboard** (operator + owner + finance use). Keyboard-heavy, dense.
2. **Buyer-facing tracking / NDR / returns pages**. Mobile-heavy, low-literacy-friendly.
3. **Internal admin console**. Power-user; less stringent.

## Goals

- All UI surfaces compliant with **WCAG 2.1 AA**.
- Buyer pages tested for low-bandwidth and screen reader use.
- Seller dashboard fully keyboard-navigable.
- High-contrast mode; adjustable text size.

## Hard rules

- Color is never the only signal (status conveyed via text + icon + color).
- Forms have visible labels (not placeholder-only).
- Interactive elements have ≥ 44×44 px touch targets on mobile.
- Focus indicators visible on all interactive elements.
- Images have alt text; decorative images marked decorative.
- Charts/graphs have data-table fallbacks.
- Modals: focus trapped; Esc closes; restored to trigger on close.
- Error messages: descriptive, near the offending field.

## Buyer-facing specifics

- Designed for **3G connections + screen readers + reduced literacy**.
- Iconography preferred over text where space-constrained.
- Multilingual + high contrast.
- Reduced motion respected (`prefers-reduced-motion`).

## Seller dashboard specifics

- Keyboard shortcuts for power users (e.g., `g s` → shipments, `b` → bulk book).
- Bulk action results (success/failure) announced via aria-live regions.
- Print stylesheet for invoices and labels.

## Testing

- Automated: axe-core in CI on critical pages.
- Manual: quarterly audit by a a11y consultant.
- Real users: pilot with diverse seller users (different levels of digital literacy).

## Open questions

- **Q-A11Y1** — Voice navigation for buyer pages (low-literacy support)? Possibly v3.
- **Q-A11Y2** — Compliance certification (e.g., Section 508 if we ever sell to govt) — defer.
