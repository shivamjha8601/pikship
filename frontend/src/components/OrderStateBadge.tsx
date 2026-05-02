import { Badge } from "@/components/ui/Badge";
import type { Order } from "@/lib/api";

export function OrderStateBadge({ state }: { state: Order["state"] }) {
  const tone =
    state === "delivered" ? "success" :
    state === "cancelled" || state === "rto" ? "danger" :
    state === "in_transit" || state === "booked" || state === "allocating" ? "accent" :
    "neutral";
  return <Badge tone={tone}>{state.replace("_", " ")}</Badge>;
}
