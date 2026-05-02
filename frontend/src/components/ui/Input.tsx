"use client";
import * as React from "react";
import { cn } from "@/lib/cn";

export const Input = React.forwardRef<
  HTMLInputElement,
  React.InputHTMLAttributes<HTMLInputElement>
>(({ className, ...props }, ref) => (
  <input
    ref={ref}
    className={cn(
      "h-10 w-full rounded-md border border-border bg-surface px-3 text-sm",
      "placeholder:text-muted",
      "focus:outline-none focus:ring-2 focus:ring-accent/30 focus:border-accent",
      "disabled:opacity-60 disabled:cursor-not-allowed",
      className,
    )}
    {...props}
  />
));
Input.displayName = "Input";

export const Textarea = React.forwardRef<
  HTMLTextAreaElement,
  React.TextareaHTMLAttributes<HTMLTextAreaElement>
>(({ className, ...props }, ref) => (
  <textarea
    ref={ref}
    className={cn(
      "min-h-[80px] w-full rounded-md border border-border bg-surface px-3 py-2 text-sm",
      "placeholder:text-muted",
      "focus:outline-none focus:ring-2 focus:ring-accent/30 focus:border-accent",
      "disabled:opacity-60",
      className,
    )}
    {...props}
  />
));
Textarea.displayName = "Textarea";

export function Label({ children, className, ...props }: React.LabelHTMLAttributes<HTMLLabelElement>) {
  return (
    <label className={cn("mb-1 block text-sm font-medium text-text", className)} {...props}>
      {children}
    </label>
  );
}

export function Field({
  label, hint, error, children,
}: {
  label?: string; hint?: string; error?: string; children: React.ReactNode;
}) {
  return (
    <div className="space-y-1">
      {label && <Label>{label}</Label>}
      {children}
      {hint && !error && <p className="text-xs text-muted">{hint}</p>}
      {error && <p className="text-xs text-danger">{error}</p>}
    </div>
  );
}
