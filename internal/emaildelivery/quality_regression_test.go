package emaildelivery

import (
	"context"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
)

func TestQualitySMTPUnicodeAndEnvelope(t *testing.T) {
	fixture := startSMTPFixture(t, "250-localhost\r\n250 SIZE 1048576", false)
	defer fixture.close()
	mailer := SMTP{Settings: smtpSettings(fixture.address)}
	if err := mailer.SendClassroomInvitation(context.Background(), "student@example.test", "Zoë", "数学", "https://school.example/?join_class=ABC123"); err != nil {
		t.Fatal(err)
	}
	raw := <-fixture.body
	message, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	if err != nil || !strings.Contains(subject, "数学") {
		t.Fatalf("subject: %q %v", subject, err)
	}
	if message.Header.Get("Content-Transfer-Encoding") != "quoted-printable" {
		t.Fatal("Unicode body has no safe transfer encoding")
	}
	body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
	if err != nil || !strings.Contains(string(body), "Zoë") || !strings.Contains(string(body), "join_class=ABC123") {
		t.Fatalf("body: %s %v", body, err)
	}
	found := false
	for len(fixture.commands) > 0 {
		if command := <-fixture.commands; strings.HasPrefix(command, "MAIL FROM:") {
			found = true
			if command != "MAIL FROM:<no-reply@example.test>" {
				t.Fatalf("invalid sender envelope: %s", command)
			}
		}
	}
	if !found {
		t.Fatal("no sender envelope")
	}
}
