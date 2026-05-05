"use client";
import * as React from "react";
import Link, { type LinkProps } from "next/link";
import { cn } from "@/lib/cn";

type Variant = "primary" | "secondary" | "ghost" | "danger";
type Size = "sm" | "md" | "lg";

const variants: Record<Variant, string> = {
  primary:
    "bg-accent text-accent-fg hover:bg-accent/90 disabled:bg-accent/40",
  secondary:
    "bg-surface border border-border text-text hover:bg-bg disabled:opacity-50",
  ghost:
    "bg-transparent text-text hover:bg-surface disabled:opacity-50",
  danger:
    "bg-danger text-white hover:bg-danger/90 disabled:opacity-50",
};

const sizes: Record<Size, string> = {
  sm: "h-8 px-3 text-sm",
  md: "h-10 px-4 text-sm",
  lg: "h-12 px-6 text-base",
};

const baseClasses =
  "inline-flex items-center justify-center gap-2 rounded-md font-medium " +
  "transition-colors focus-visible:outline focus-visible:outline-2 " +
  "focus-visible:outline-accent disabled:pointer-events-none";

export function buttonClasses({
  variant = "primary",
  size = "md",
  className,
}: { variant?: Variant; size?: Size; className?: string } = {}): string {
  return cn(baseClasses, variants[variant], sizes[size], className);
}

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant = "primary", size = "md", loading, disabled, children, ...props }, ref) => {
    return (
      <button
        ref={ref}
        disabled={disabled || loading}
        className={buttonClasses({ variant, size, className })}
        {...props}
      >
        {loading && (
          <span className="h-3 w-3 animate-spin rounded-full border-2 border-current border-t-transparent" />
        )}
        {children}
      </button>
    );
  },
);
Button.displayName = "Button";

// LinkButton renders a Next <Link> with button styling. Use this anywhere
// you'd otherwise write <Link><Button>...</Button></Link> — that pattern
// nests a <button> inside an <a> which is invalid HTML and breaks
// keyboard / right-click "Open in new tab" semantics.
export interface LinkButtonProps
  extends Omit<LinkProps, "as">,
    Pick<React.AnchorHTMLAttributes<HTMLAnchorElement>, "className" | "target" | "rel" | "aria-label"> {
  variant?: Variant;
  size?: Size;
  children: React.ReactNode;
}

export function LinkButton({
  variant = "primary",
  size = "md",
  className,
  children,
  ...props
}: LinkButtonProps) {
  return (
    <Link className={buttonClasses({ variant, size, className })} {...props}>
      {children}
    </Link>
  );
}
