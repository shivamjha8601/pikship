"use client";
import * as React from "react";
import { Check, Loader2 } from "lucide-react";
import { Input } from "./Input";
import { lookupPincode, isValidPincode } from "@/lib/pincode";

// 6-digit Indian pincode input. When the input becomes a valid 6-digit
// pincode we debounce 250ms and call api.postalpincode.in. On success we
// fire onResolve so the parent can autofill city + state — but we never
// disable those fields, the user can override.

export interface PincodeInputProps {
  value: string;
  onChange: (next: string) => void;
  onResolve?: (data: { city: string; state: string }) => void;
  required?: boolean;
  id?: string;
  name?: string;
  autoFocus?: boolean;
  className?: string;
}

type Status = "idle" | "looking" | "found" | "not_found";

export function PincodeInput({
  value, onChange, onResolve, required, ...rest
}: PincodeInputProps) {
  const [status, setStatus] = React.useState<Status>("idle");
  const [resolved, setResolved] = React.useState<{ city: string; state: string } | null>(null);
  // Keep the latest onResolve in a ref so the lookup effect doesn't re-fire
  // every time the parent re-renders with a new closure.
  const onResolveRef = React.useRef(onResolve);
  React.useEffect(() => { onResolveRef.current = onResolve; }, [onResolve]);

  React.useEffect(() => {
    if (!isValidPincode(value)) {
      setStatus("idle");
      setResolved(null);
      return;
    }
    const ctrl = new AbortController();
    setStatus("looking");
    const t = setTimeout(async () => {
      const data = await lookupPincode(value, ctrl.signal);
      if (ctrl.signal.aborted) return;
      if (data) {
        setStatus("found");
        setResolved({ city: data.city, state: data.state });
        onResolveRef.current?.({ city: data.city, state: data.state });
      } else {
        setStatus("not_found");
        setResolved(null);
      }
    }, 250);
    return () => {
      clearTimeout(t);
      ctrl.abort();
    };
  }, [value]);

  return (
    <div className="space-y-1">
      <Input
        type="text"
        inputMode="numeric"
        pattern="[1-9][0-9]{5}"
        title="6-digit Indian pincode"
        maxLength={6}
        placeholder="560001"
        value={value}
        required={required}
        onChange={(e) => onChange(e.target.value.replace(/\D/g, "").slice(0, 6))}
        {...rest}
      />
      <div className="min-h-[1rem] text-xs">
        {status === "looking" && (
          <span className="inline-flex items-center gap-1 text-muted">
            <Loader2 className="h-3 w-3 animate-spin" />
            Looking up…
          </span>
        )}
        {status === "found" && resolved && (
          <span className="inline-flex items-center gap-1 text-success">
            <Check className="h-3 w-3" />
            {resolved.city}, {resolved.state}
          </span>
        )}
        {status === "not_found" && (
          <span className="text-muted">
            Couldn&apos;t auto-fill city/state — you can still use this pincode.
          </span>
        )}
        {status === "idle" && (
          <span className="text-muted">6 digits — we'll fill in city & state.</span>
        )}
      </div>
    </div>
  );
}
