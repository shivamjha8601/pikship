"use client";
import * as React from "react";
import { cn } from "@/lib/cn";

// A tiny click-anchored dropdown. Closes on outside click and on Escape.
// Not a full headless-ui — we don't need keyboard arrow navigation for
// the avatar menu (1-2 items) — but it handles the cases that bite users:
// click-outside dismissal, Escape, and proper aria-expanded.

export function Menu({
  trigger,
  align = "right",
  children,
}: {
  trigger: (props: { open: boolean; onClick: () => void; ref: React.Ref<HTMLButtonElement> }) => React.ReactNode;
  align?: "left" | "right";
  children: React.ReactNode | ((close: () => void) => React.ReactNode);
}) {
  const [open, setOpen] = React.useState(false);
  const triggerRef = React.useRef<HTMLButtonElement>(null);
  const popRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    if (!open) return;
    function onDocClick(e: MouseEvent) {
      if (
        popRef.current?.contains(e.target as Node) ||
        triggerRef.current?.contains(e.target as Node)
      ) return;
      setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onDocClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDocClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const close = React.useCallback(() => setOpen(false), []);
  const content = typeof children === "function" ? children(close) : children;

  return (
    <div className="relative">
      {trigger({ open, onClick: () => setOpen((o) => !o), ref: triggerRef })}
      {open && (
        <div
          ref={popRef}
          role="menu"
          className={cn(
            "absolute z-30 mt-2 min-w-[12rem] rounded-md border border-border bg-surface p-1 shadow-lg",
            align === "right" ? "right-0" : "left-0",
          )}
        >
          {content}
        </div>
      )}
    </div>
  );
}

export function MenuItem({
  onClick,
  destructive,
  children,
}: {
  onClick?: () => void;
  destructive?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      role="menuitem"
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2 rounded px-3 py-2 text-left text-sm",
        destructive ? "text-danger hover:bg-danger/5" : "text-text hover:bg-bg",
      )}
    >
      {children}
    </button>
  );
}

export function MenuSeparator() {
  return <div className="my-1 h-px bg-border" />;
}
