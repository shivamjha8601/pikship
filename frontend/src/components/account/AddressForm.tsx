"use client";
import * as React from "react";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { PhoneInput, isValidIndianMobile } from "@/components/ui/PhoneInput";
import { PincodeInput } from "@/components/ui/PincodeInput";
import type { BuyerAddressInput } from "@/lib/api";

export type AddressFormValue = BuyerAddressInput;

export const EMPTY_ADDRESS: AddressFormValue = {
  label: "",
  buyer_name: "",
  buyer_phone: "+91",
  buyer_email: "",
  address: { line1: "", line2: "", city: "", state: "", country: "IN", pincode: "" },
  pincode: "",
  state: "",
  is_default: false,
};

export function isAddressValid(v: AddressFormValue): boolean {
  return (
    v.label.trim().length > 0 &&
    v.buyer_name.trim().length > 0 &&
    isValidIndianMobile(v.buyer_phone) &&
    v.address.line1.trim().length > 0 &&
    v.address.city.trim().length > 0 &&
    v.address.state.trim().length > 0 &&
    /^[1-9]\d{5}$/.test(v.pincode)
  );
}

export function AddressForm({
  value,
  onChange,
  onSubmit,
  onCancel,
  submitLabel = "Save address",
  submitting = false,
  showDefaultToggle = true,
}: {
  value: AddressFormValue;
  onChange: (next: AddressFormValue) => void;
  onSubmit: () => void;
  onCancel?: () => void;
  submitLabel?: string;
  submitting?: boolean;
  showDefaultToggle?: boolean;
}) {
  function patch(p: Partial<AddressFormValue>) {
    onChange({ ...value, ...p });
  }
  function patchAddr(p: Partial<AddressFormValue["address"]>) {
    onChange({ ...value, address: { ...value.address, ...p } });
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (isAddressValid(value)) onSubmit();
      }}
      className="space-y-4"
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Label" hint="e.g. Mom's home, Bangalore office">
          <Input
            required
            placeholder="Home"
            value={value.label}
            onChange={(e) => patch({ label: e.target.value })}
          />
        </Field>
        <Field label="Recipient name">
          <Input
            required
            value={value.buyer_name}
            onChange={(e) => patch({ buyer_name: e.target.value })}
          />
        </Field>
        <Field
          label="Phone"
          error={
            value.buyer_phone !== "+91" && !isValidIndianMobile(value.buyer_phone)
              ? "10 digits, starts with 6/7/8/9"
              : undefined
          }
        >
          <PhoneInput
            value={value.buyer_phone}
            onChange={(v) => patch({ buyer_phone: v })}
            required
          />
        </Field>
        <Field label="Email" hint="Optional">
          <Input
            type="email"
            value={value.buyer_email || ""}
            onChange={(e) => patch({ buyer_email: e.target.value })}
          />
        </Field>
        <Field label="Address line 1">
          <Input
            required
            value={value.address.line1}
            onChange={(e) => patchAddr({ line1: e.target.value })}
          />
        </Field>
        <Field label="Address line 2" hint="Optional">
          <Input
            value={value.address.line2 || ""}
            onChange={(e) => patchAddr({ line2: e.target.value })}
          />
        </Field>
        <Field label="Pincode">
          <PincodeInput
            value={value.pincode}
            onChange={(v) =>
              onChange({
                ...value,
                pincode: v,
                address: { ...value.address, pincode: v },
              })
            }
            onResolve={({ city, state }) =>
              onChange({
                ...value,
                address: {
                  ...value.address,
                  city: value.address.city || city,
                  state: value.address.state || state,
                },
                state: value.state || state,
              })
            }
            required
          />
        </Field>
        <Field label="City">
          <Input
            required
            value={value.address.city}
            onChange={(e) => patchAddr({ city: e.target.value })}
          />
        </Field>
        <Field label="State">
          <Input
            required
            value={value.address.state}
            onChange={(e) =>
              onChange({
                ...value,
                state: e.target.value,
                address: { ...value.address, state: e.target.value },
              })
            }
          />
        </Field>
      </div>

      {showDefaultToggle && (
        <label className="inline-flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={value.is_default}
            onChange={(e) => patch({ is_default: e.target.checked })}
            className="h-4 w-4 rounded border-border text-accent focus:ring-accent"
          />
          Set as default ship-to address
        </label>
      )}

      <div className="flex items-center justify-end gap-2 pt-2">
        {onCancel && (
          <Button type="button" variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
        )}
        <Button type="submit" loading={submitting} disabled={!isAddressValid(value)}>
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}
