package notifications

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

type captureSMS struct{ last string }

func (c *captureSMS) SendSMS(_ context.Context, to, message, _ string) error {
	c.last = to + "|" + message
	return nil
}

type captureEmail struct{ last EmailMessage }

func (c *captureEmail) Send(_ context.Context, msg EmailMessage) error {
	c.last = msg
	return nil
}

func nopLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestDispatch_shipmentBooked_sms(t *testing.T) {
	sms := &captureSMS{}
	svc := New(sms, nil, nopLog())

	err := svc.Dispatch(context.Background(), Notification{
		Kind:           KindShipmentBooked,
		RecipientPhone: "+919999999999",
		Payload: map[string]string{
			"order_id": "ORD-001",
			"awb":      "DEL12345",
		},
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sms.last == "" {
		t.Fatal("expected SMS to be sent")
	}
	if !contains(sms.last, "DEL12345") {
		t.Errorf("SMS should contain AWB, got: %s", sms.last)
	}
}

func TestDispatch_shipmentBooked_email(t *testing.T) {
	email := &captureEmail{}
	svc := New(nil, email, nopLog())

	_ = svc.Dispatch(context.Background(), Notification{
		Kind:           KindShipmentBooked,
		RecipientEmail: "buyer@example.com",
		RecipientName:  "Ramesh",
		Payload: map[string]string{
			"order_id": "ORD-002",
			"awb":      "DEL99999",
		},
	})
	if email.last.Subject == "" {
		t.Fatal("expected email to be sent")
	}
	if len(email.last.To) == 0 || email.last.To[0] != "buyer@example.com" {
		t.Errorf("wrong recipient: %v", email.last.To)
	}
}

func TestDispatch_unknownKind(t *testing.T) {
	svc := New(nil, nil, nopLog())
	err := svc.Dispatch(context.Background(), Notification{Kind: "totally.unknown"})
	if err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestDispatch_ndrOpened(t *testing.T) {
	sms := &captureSMS{}
	svc := New(sms, nil, nopLog())
	_ = svc.Dispatch(context.Background(), Notification{
		Kind:           KindNDROpened,
		RecipientPhone: "+919999999999",
		Payload:        map[string]string{"order_id": "ORD-003", "reason": "nobody home"},
	})
	if !contains(sms.last, "nobody home") {
		t.Errorf("SMS should contain reason, got: %s", sms.last)
	}
}

func TestTemplates_allKindsCovered(t *testing.T) {
	kinds := []MessageKind{
		KindShipmentBooked, KindShipmentDelivered, KindNDROpened,
		KindRTOInitiated, KindInviteSent, KindKYCApproved, KindKYCRejected,
	}
	for _, k := range kinds {
		if _, ok := templates[k]; !ok {
			t.Errorf("no template for kind %s", k)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}
