package emaildelivery

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/ayush/slate/internal/config"
)

type smtpFixture struct {
	address  string
	commands chan string
	body     chan string
	close    func()
}

func startSMTPFixture(t *testing.T, ehlo string, stallAfterEHLO bool) smtpFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	commands := make(chan string, 16)
	body := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		write := func(response string) bool {
			_, err := writer.WriteString(response + "\r\n")
			if err == nil {
				err = writer.Flush()
			}
			return err == nil
		}
		if !write("220 localhost ESMTP ready") {
			return
		}
		var captured strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			command := strings.TrimSpace(line)
			commands <- command
			upper := strings.ToUpper(command)
			switch {
			case strings.HasPrefix(upper, "EHLO "):
				if stallAfterEHLO {
					continue
				}
				if !write(ehlo) {
					return
				}
			case strings.HasPrefix(upper, "AUTH PLAIN "):
				if !write("235 2.7.0 authenticated") {
					return
				}
			case strings.HasPrefix(upper, "MAIL FROM:"):
				if !write("250 2.1.0 sender ok") {
					return
				}
			case strings.HasPrefix(upper, "RCPT TO:"):
				if !write("250 2.1.5 recipient ok") {
					return
				}
			case upper == "DATA":
				if !write("354 end with dot") {
					return
				}
				for {
					dataLine, readErr := reader.ReadString('\n')
					if readErr != nil {
						return
					}
					dataLine = strings.TrimRight(dataLine, "\r\n")
					if dataLine == "." {
						break
					}
					captured.WriteString(dataLine)
					captured.WriteByte('\n')
				}
				body <- captured.String()
				if !write("250 2.0.0 queued") {
					return
				}
			case upper == "QUIT":
				_ = write("221 2.0.0 bye")
				return
			case strings.HasPrefix(upper, "STARTTLS"):
				commands <- "STARTTLS-OBSERVED"
				_ = write("220 2.0.0 begin TLS")
				return
			default:
				if !write("250 ok") {
					return
				}
			}
		}
	}()
	return smtpFixture{
		address: listener.Addr().String(), commands: commands, body: body,
		close: func() {
			_ = listener.Close()
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		},
	}
}

func smtpSettings(address string) config.Settings {
	host, portRaw, _ := net.SplitHostPort(address)
	var port int
	_, _ = fmt.Sscanf(portRaw, "%d", &port)
	return config.Settings{SMTPHost: host, SMTPPort: port, SMTPFrom: "Slate <no-reply@example.test>"}
}

func TestSMTPDeliversPasswordResetAndInvitationThroughLocalServer(t *testing.T) {
	fixture := startSMTPFixture(t, "250-localhost\r\n250 SIZE 1048576", false)
	defer fixture.close()
	settings := smtpSettings(fixture.address)
	settings.SMTPUsername, settings.SMTPPassword = "mailer", "secret"
	mailer := SMTP{Settings: settings}
	if err := mailer.SendPasswordReset(context.Background(), "student@example.test", "A Student", "https://slate.test/login?token=abc"); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-fixture.body:
		for _, want := range []string{"To: student@example.test", "Subject: Reset your Slate password", "https://slate.test/login?token=abc"} {
			if !strings.Contains(body, want) {
				t.Fatalf("SMTP body missing %q: %s", want, body)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP server did not receive message body")
	}
	authCommand := ""
	for len(fixture.commands) > 0 {
		command := <-fixture.commands
		if strings.HasPrefix(command, "AUTH PLAIN ") {
			authCommand = command
			break
		}
	}
	if authCommand == "" {
		t.Fatal("configured credentials were not sent")
	}
	encoded := strings.TrimPrefix(authCommand, "AUTH PLAIN ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != "\x00mailer\x00secret" {
		t.Fatalf("SMTP auth payload = %q (%v)", decoded, err)
	}
}

func TestSMTPRequiresTLSForNonLoopbackAndUsesSTARTTLSWhenOffered(t *testing.T) {
	fixture := startSMTPFixture(t, "250-localhost\r\n250-STARTTLS\r\n250 SIZE 1048576", false)
	defer fixture.close()
	err := (SMTP{Settings: smtpSettings(fixture.address)}).SendClassroomInvitation(context.Background(), "student@example.test", "A Student", "Physics", "https://slate.test/?join_class=abc")
	if err == nil || !strings.Contains(err.Error(), "STARTTLS failed") {
		t.Fatalf("STARTTLS handshake failure = %v", err)
	}
	observed := false
	for len(fixture.commands) > 0 {
		if <-fixture.commands == "STARTTLS-OBSERVED" {
			observed = true
			break
		}
	}
	if !observed {
		t.Fatal("client did not issue STARTTLS")
	}
}

func TestSMTPContextCancellationInterruptsProtocolRead(t *testing.T) {
	fixture := startSMTPFixture(t, "", true)
	defer fixture.close()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		finished <- (SMTP{Settings: smtpSettings(fixture.address)}).SendPasswordReset(ctx, "student@example.test", "Student", "https://slate.test/login?token=x")
	}()
	select {
	case command := <-fixture.commands:
		if !strings.HasPrefix(command, "EHLO ") {
			t.Fatalf("first SMTP command = %q", command)
		}
	case <-time.After(time.Second):
		t.Fatal("client did not reach SMTP greeting")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled SMTP delivery returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP protocol read ignored context cancellation")
	}
}
