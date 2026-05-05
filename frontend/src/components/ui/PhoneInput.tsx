"use client";
import * as React from "react";
import { cn } from "@/lib/cn";

// Indian mobile input with a locked "+91" prefix. The parent stores the
// canonical "+91XXXXXXXXXX" form (or "+91" while incomplete). The user can
// only type/paste the 10 digits — they can never erase the prefix.
//
// Validation: exactly 10 digits, starting with 6/7/8/9 (TRAI mobile range).

const RX_VALID = /^\+91[6-9]\d{9}$/;

export function isValidIndianMobile(value: string): boolean {
  return RX_VALID.test(value);
}

// Strip everything except the trailing 10 digits, regardless of how the
// caller stored it (e.g. "+91 98765 43210", "919876543210", "98765-43210").
function digitsOnly(s: string): string {
  return s.replace(/^\+?91/, "").replace(/\D/g, "").slice(0, 10);
}

export interface PhoneInputProps {
  value: string;
  onChange: (next: string) => void;
  required?: boolean;
  autoFocus?: boolean;
  disabled?: boolean;
  id?: string;
  name?: string;
  className?: string;
  "aria-label"?: string;
}

export const PhoneInput = React.forwardRef<HTMLInputElement, PhoneInputProps>(
  ({ value, onChange, required, disabled, className, ...rest }, ref) => {
    const digits = digitsOnly(value);

    function handleChange(e: React.ChangeEvent<HTMLInputElement>) {
      const next = digitsOnly(e.target.value);
      onChange(next.length > 0 ? `+91${next}` : "+91");
    }

    return (
      <div
        className={cn(
          "flex h-10 items-stretch overflow-hidden rounded-md border border-border bg-surface text-sm",
          "focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/30",
          disabled && "opacity-60",
          className,
        )}
      >
        <span
          aria-hidden="true"
          className="flex select-none items-center border-r border-border bg-bg/60 px-3 font-medium text-muted"
        >
          +91
        </span>
        <input
          ref={ref}
          type="tel"
          inputMode="numeric"
          autoComplete="tel-national"
          // Native validity message kicks in on submit if pattern fails.
          pattern="[6-9][0-9]{9}"
          title="10-digit Indian mobile starting with 6, 7, 8, or 9"
          value={digits}
          onChange={handleChange}
          required={required}
          disabled={disabled}
          placeholder="98765 43210"
          maxLength={10}
          className="w-full bg-transparent px-3 outline-none placeholder:text-muted disabled:cursor-not-allowed"
          {...rest}
        />
      </div>
    );
  },
);
PhoneInput.displayName = "PhoneInput";
