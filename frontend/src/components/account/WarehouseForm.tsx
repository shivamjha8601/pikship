"use client";
import * as React from "react";
import { Button } from "@/components/ui/Button";
import { Field, Input } from "@/components/ui/Input";
import { PhoneInput, isValidIndianMobile } from "@/components/ui/PhoneInput";
import { PincodeInput } from "@/components/ui/PincodeInput";
import type { PickupLocation } from "@/lib/api";

// PickupLocation has a stable wire shape; we slice off server-managed fields
// and use this as the form value type. Matches catalogApi.createPickup's input.
export type WarehouseFormValue = Omit<
  PickupLocation,
  "id" | "seller_id" | "created_at" | "updated_at"
>;

export const EMPTY_WAREHOUSE: WarehouseFormValue = {
  label: "",
  contact_name: "",
  contact_phone: "+91",
  contact_email: "",
  address: { line1: "", line2: "", city: "", state: "", country: "IN", pincode: "" },
  pincode: "",
  state: "",
  pickup_hours: "",
  gstin: "",
  active: true,
  is_default: false,
};

export function isWarehouseValid(v: WarehouseFormValue): boolean {
  return (
    v.label.trim().length >= 2 &&
    v.contact_name.trim().length > 0 &&
    isValidIndianMobile(v.contact_phone) &&
    v.address.line1.trim().length > 0 &&
    v.address.city.trim().length > 0 &&
    v.address.state.trim().length > 0 &&
    /^[1-9]\d{5}$/.test(v.pincode)
  );
}

export function WarehouseForm({
  value,
  onChange,
  onSubmit,
  onCancel,
  submitLabel = "Save warehouse",
  submitting = false,
  showDefaultToggle = true,
}: {
  value: WarehouseFormValue;
  onChange: (next: WarehouseFormValue) => void;
  onSubmit: () => void;
  onCancel?: () => void;
  submitLabel?: string;
  submitting?: boolean;
  showDefaultToggle?: boolean;
}) {
  function patch(p: Partial<WarehouseFormValue>) {
    onChange({ ...value, ...p });
  }
  function patchAddr(p: Partial<WarehouseFormValue["address"]>) {
    onChange({ ...value, address: { ...value.address, ...p } });
  }

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (isWarehouseValid(value)) onSubmit();
      }}
      className="space-y-4"
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Label" hint="e.g. Home, Bandra warehouse">
          <Input
            required
            minLength={2}
            placeholder="Home"
            value={value.label}
            onChange={(e) => patch({ label: e.target.value })}
          />
        </Field>
        <Field label="GSTIN" hint="Optional. Required if you bill from this address.">
          <Input
            maxLength={15}
            placeholder="29AABCU9603R1ZX"
            value={value.gstin || ""}
            onChange={(e) => patch({ gstin: e.target.value.toUpperCase() })}
          />
        </Field>
        <Field label="Contact name">
          <Input
            required
            value={value.contact_name}
            onChange={(e) => patch({ contact_name: e.target.value })}
          />
        </Field>
        <Field
          label="Contact phone"
          error={
            value.contact_phone !== "+91" && !isValidIndianMobile(value.contact_phone)
              ? "10 digits, starts with 6/7/8/9"
              : undefined
          }
        >
          <PhoneInput
            value={value.contact_phone}
            onChange={(v) => patch({ contact_phone: v })}
            required
          />
        </Field>
        <Field label="Contact email" hint="Optional">
          <Input
            type="email"
            value={value.contact_email || ""}
            onChange={(e) => patch({ contact_email: e.target.value })}
          />
        </Field>
        <Field label="Pickup hours" hint="Optional, e.g. 10am–6pm">
          <Input
            value={value.pickup_hours || ""}
            onChange={(e) => patch({ pickup_hours: e.target.value })}
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
          Set as default pickup warehouse
        </label>
      )}

      <div className="flex items-center justify-end gap-2 pt-2">
        {onCancel && (
          <Button type="button" variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
        )}
        <Button type="submit" loading={submitting} disabled={!isWarehouseValid(value)}>
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}
