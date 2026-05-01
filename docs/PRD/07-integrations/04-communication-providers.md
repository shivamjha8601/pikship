# Communication providers

> WhatsApp, SMS, email, push. Pairs with `04-features/16-notifications.md`.

## WhatsApp Business

- Meta's WhatsApp Business Platform (BSP-mediated).
- BSP options: Gupshup, 360Dialog, Karix, AiSensy, Twilio.
- Multi-vendor: at least 2 for resilience.
- Templates require Meta approval (typ. 24–48h).
- Per reseller: separate WhatsApp business sender (regulatory).

### Capabilities used

- Template messages (transactional).
- Session messages (inside 24h window).
- Delivery + read receipts.
- 2-way messaging (planned v2 for NDR chatbot).
- Media attachments (photos for NDR / returns; PDFs for invoices).

## SMS

- DLT-registered (regulatory in India).
- Vendors: MSG91, Karix, Twilio India, Gupshup.
- Per reseller: separate DLT sender ID + templates.
- Used for OTP and as fallback to WhatsApp.

## Email (transactional)

- Vendors: SendGrid, AWS SES, Postmark.
- DKIM/SPF/DMARC per reseller domain.
- Reputation monitoring; bounce + complaint handling.

## Push (v2)

- FCM (Android), APNs (iOS) when mobile app launches.

## Internal (ops)

- Slack, PagerDuty for ops alerts.

## Cost optimization

- WhatsApp typ. ₹0.30–₹0.85 per template send (varies by category).
- SMS typ. ₹0.10–₹0.20 per message.
- Email typ. < ₹0.01 per email.
- Channel routing prefers WhatsApp where consented (better engagement) but cost-aware for low-priority.

## Failover

- WhatsApp delivery fail → SMS retry within 5 min.
- SMS fail → email if available.
- All sends idempotent on `(event_id, recipient, channel, template)`.

## Open questions

- **Q-CMP1** — Voice/IVR for low-literacy buyers? Possibly v3.
- **Q-CMP2** — RCS (Rich Communication Services) as SMS evolution? Watch market.
- **Q-CMP3** — In-app push notification before mobile app — via web push? Default: web push v2.
