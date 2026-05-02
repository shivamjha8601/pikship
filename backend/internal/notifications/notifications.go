// Package notifications dispatches transactional messages (SMS, email)
// for shipment lifecycle events. Per LLD §03-services/20-notifications.
package notifications

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/vishal1132/pikshipp/backend/internal/core"
)

// MessageKind is the type of notification.
type MessageKind string

const (
	KindShipmentBooked    MessageKind = "shipment.booked"
	KindShipmentPickedUp  MessageKind = "shipment.picked_up"
	KindShipmentDelivered MessageKind = "shipment.delivered"
	KindNDROpened         MessageKind = "ndr.opened"
	KindRTOInitiated      MessageKind = "rto.initiated"
	KindCODRemitted       MessageKind = "cod.remitted"
	KindInviteSent        MessageKind = "seller.invite_sent"
	KindKYCApproved       MessageKind = "seller.kyc_approved"
	KindKYCRejected       MessageKind = "seller.kyc_rejected"
)

// SMSSender sends a transactional SMS.
type SMSSender interface {
	SendSMS(ctx context.Context, to, message, templateID string) error
}

// EmailSender sends a transactional email.
type EmailSender interface {
	Send(ctx context.Context, msg EmailMessage) error
}

// EmailMessage is a transactional email.
type EmailMessage struct {
	To      []string
	Subject string
	HTML    string
	Text    string
}

// Notification is one dispatch request.
type Notification struct {
	Kind      MessageKind
	SellerID  *core.SellerID
	RecipientPhone string
	RecipientEmail string
	RecipientName  string
	Payload   map[string]string
	CreatedAt time.Time
}

// Service dispatches notifications.
type Service interface {
	Dispatch(ctx context.Context, n Notification) error
}

type service struct {
	sms   SMSSender
	email EmailSender
	log   *slog.Logger
}

// New constructs the notifications service.
func New(sms SMSSender, email EmailSender, log *slog.Logger) Service {
	return &service{sms: sms, email: email, log: log}
}

func (s *service) Dispatch(ctx context.Context, n Notification) error {
	tmpl, ok := templates[n.Kind]
	if !ok {
		return fmt.Errorf("notifications.Dispatch: no template for %s", n.Kind)
	}

	if n.RecipientPhone != "" && s.sms != nil {
		msg := tmpl.renderSMS(n.Payload)
		if err := s.sms.SendSMS(ctx, n.RecipientPhone, msg, tmpl.SMSTemplateID); err != nil {
			s.log.WarnContext(ctx, "notifications: SMS failed",
				slog.String("kind", string(n.Kind)),
				slog.String("err", err.Error()))
		}
	}
	if n.RecipientEmail != "" && s.email != nil {
		if err := s.email.Send(ctx, EmailMessage{
			To:      []string{n.RecipientEmail},
			Subject: tmpl.renderSubject(n.Payload),
			HTML:    tmpl.renderHTML(n.Payload, n.RecipientName),
			Text:    tmpl.renderSMS(n.Payload),
		}); err != nil {
			s.log.WarnContext(ctx, "notifications: email failed",
				slog.String("kind", string(n.Kind)),
				slog.String("err", err.Error()))
		}
	}
	return nil
}

// template is a message template for one MessageKind.
type template struct {
	SMSTemplateID string
	SMSBody       string
	SubjectTmpl   string
	HTMLBodyTmpl  string
}

func (t template) renderSMS(p map[string]string) string {
	return interpolate(t.SMSBody, p)
}

func (t template) renderSubject(p map[string]string) string {
	return interpolate(t.SubjectTmpl, p)
}

func (t template) renderHTML(p map[string]string, name string) string {
	if name != "" {
		p["name"] = name
	}
	return "<p>" + interpolate(t.HTMLBodyTmpl, p) + "</p>"
}

func interpolate(s string, p map[string]string) string {
	out := s
	for k, v := range p {
		out = replaceAll(out, "{"+k+"}", v)
	}
	return out
}

func replaceAll(s, old, new string) string {
	result := ""
	for i := 0; i < len(s); {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			result += new
			i += len(old)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}

// templates is the static template registry.
var templates = map[MessageKind]template{
	KindShipmentBooked: {
		SMSTemplateID: "shipment_booked",
		SMSBody:       "Your order {order_id} has been booked with AWB {awb}. Track at pikshipp.com/track/{awb}",
		SubjectTmpl:   "Your shipment {awb} is booked",
		HTMLBodyTmpl:  "Your order {order_id} has been booked. AWB: {awb}",
	},
	KindShipmentDelivered: {
		SMSTemplateID: "shipment_delivered",
		SMSBody:       "Your order {order_id} has been delivered. Thank you!",
		SubjectTmpl:   "Order {order_id} delivered",
		HTMLBodyTmpl:  "Your order {order_id} has been successfully delivered.",
	},
	KindNDROpened: {
		SMSTemplateID: "ndr_opened",
		SMSBody:       "Delivery attempt failed for order {order_id}. Reason: {reason}. Reply to reschedule.",
		SubjectTmpl:   "Delivery attempt failed — order {order_id}",
		HTMLBodyTmpl:  "We were unable to deliver your order {order_id}. Reason: {reason}.",
	},
	KindRTOInitiated: {
		SMSTemplateID: "rto_initiated",
		SMSBody:       "Your order {order_id} is being returned. Contact support for help.",
		SubjectTmpl:   "Order {order_id} return initiated",
		HTMLBodyTmpl:  "Your order {order_id} is being returned to the seller.",
	},
	KindInviteSent: {
		SMSTemplateID: "invite_sent",
		SMSBody:       "You've been invited to join {seller_name} on Pikshipp. Click: {invite_url}",
		SubjectTmpl:   "You're invited to join {seller_name} on Pikshipp",
		HTMLBodyTmpl:  "Hi {name}, you've been invited to join {seller_name}. Accept here: {invite_url}",
	},
	KindKYCApproved: {
		SMSTemplateID: "kyc_approved",
		SMSBody:       "Congratulations! Your KYC has been approved. Your account is now active.",
		SubjectTmpl:   "KYC approved — your Pikshipp account is active",
		HTMLBodyTmpl:  "Your KYC application has been approved. Welcome to Pikshipp!",
	},
	KindKYCRejected: {
		SMSTemplateID: "kyc_rejected",
		SMSBody:       "Your KYC was rejected. Reason: {reason}. Please resubmit.",
		SubjectTmpl:   "KYC rejected — action required",
		HTMLBodyTmpl:  "Your KYC was rejected. Reason: {reason}. Please resubmit your documents.",
	},
}
