// India Post pincode → district + state lookup.
// The public api.postalpincode.in service is unauthenticated, returns
// district + state + post office list keyed on a 6-digit pincode. We cache
// in memory so re-renders don't refire identical requests.

export type PincodeData = {
  city: string;     // we use District as the user-facing "city"
  state: string;
  offices: string[];
};

const cache = new Map<string, PincodeData | null>();

const PIN_RX = /^[1-9]\d{5}$/;

export function isValidPincode(pin: string): boolean {
  return PIN_RX.test(pin);
}

type PostOffice = { Name: string; District: string; State: string };
type ApiEntry = { Status: string; PostOffice?: PostOffice[] };

export async function lookupPincode(
  pin: string,
  signal?: AbortSignal,
): Promise<PincodeData | null> {
  if (!isValidPincode(pin)) return null;
  if (cache.has(pin)) return cache.get(pin) ?? null;

  try {
    const res = await fetch(`https://api.postalpincode.in/pincode/${pin}`, { signal });
    if (!res.ok) return null;
    const json = (await res.json()) as ApiEntry[];
    const first = json[0];
    if (!first || first.Status !== "Success" || !first.PostOffice?.length) {
      cache.set(pin, null);
      return null;
    }
    const data: PincodeData = {
      city: first.PostOffice[0].District,
      state: first.PostOffice[0].State,
      offices: first.PostOffice.map((p) => p.Name),
    };
    cache.set(pin, data);
    return data;
  } catch {
    return null;
  }
}
