// Package emaildelivery contains the optional SMTP adapter used by password
// reset and classroom invitation flows.
package emaildelivery

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/ayush/slate/internal/config"
)

type SMTP struct{ Settings config.Settings }

func (s SMTP) SendPasswordReset(ctx context.Context, email, name, link string) error {
	return s.send(ctx, email, "Reset your Slate password", fmt.Sprintf("Hi %s,\n\nUse this link to reset your password. It expires in one hour and can be used once:\n%s\n\nIf you did not request a reset, you can ignore this email.\n", greeting(name), link))
}

func (s SMTP) SendClassroomInvitation(ctx context.Context, email, name, classroomName, link string) error {
	return s.send(ctx, email, "Invitation to join "+safeHeader(classroomName), fmt.Sprintf("Hi %s,\n\nYou have been invited to join %s on Slate. Join using this link:\n%s\n", greeting(name), classroomName, link))
}

func (s SMTP) send(ctx context.Context, to, subject, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	host := strings.TrimSpace(s.Settings.SMTPHost)
	from := strings.TrimSpace(s.Settings.SMTPFrom)
	if host == "" || from == "" {
		return fmt.Errorf("SMTP delivery is not configured")
	}
	port := s.Settings.SMTPPort
	if port <= 0 {
		port = 587
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(15 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stopCancel()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("SMTP STARTTLS failed: %w", err)
		}
	} else if !isLoopbackSMTPHost(host) {
		return fmt.Errorf("SMTP server does not offer STARTTLS")
	}
	if s.Settings.SMTPUsername != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Settings.SMTPUsername, s.Settings.SMTPPassword, host)); err != nil {
			return fmt.Errorf("SMTP authentication failed: %w", err)
		}
	}
	message := "From: " + safeHeader(from) + "\r\n" +
		"To: " + safeHeader(to) + "\r\n" +
		"Subject: " + safeHeader(subject) + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := io.WriteString(writer, message); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func isLoopbackSMTPHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func greeting(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "there"
	}
	return strings.ReplaceAll(strings.ReplaceAll(name, "\r", ""), "\n", "")
}

func safeHeader(value string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(strings.TrimSpace(value))
}
