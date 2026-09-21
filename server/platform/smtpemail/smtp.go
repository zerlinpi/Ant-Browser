// Package smtpemail implements the notification email transport using the Go
// standard library. TLS is required by default; plaintext SMTP must be an
// explicit development-only choice at configuration time.
package smtpemail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

const (
	TLSStartTLS = "starttls"
	TLSImplicit = "tls"
	TLSPlain    = "plain"
)

type Config struct {
	Address  string
	Username string
	Password string
	From     string
	TLSMode  string
	Timeout  time.Duration
}

type Sender struct {
	config Config
	host   string
	from   *mail.Address
}

func New(config Config) (*Sender, error) {
	config.Address = strings.TrimSpace(config.Address)
	config.Username = strings.TrimSpace(config.Username)
	config.From = strings.TrimSpace(config.From)
	config.TLSMode = strings.ToLower(strings.TrimSpace(config.TLSMode))
	if config.TLSMode == "" {
		config.TLSMode = TLSStartTLS
	}
	if config.Timeout == 0 {
		config.Timeout = 15 * time.Second
	}
	if config.Address == "" || config.From == "" {
		return nil, errors.New("SMTP address and from address are required")
	}
	host, _, err := net.SplitHostPort(config.Address)
	if err != nil || strings.TrimSpace(host) == "" {
		return nil, errors.New("SMTP address must use host:port format")
	}
	from, err := mail.ParseAddress(config.From)
	if err != nil {
		return nil, errors.New("SMTP from address is invalid")
	}
	if config.Username == "" && config.Password != "" {
		return nil, errors.New("SMTP username is required when a password is configured")
	}
	if config.TLSMode != TLSStartTLS && config.TLSMode != TLSImplicit && config.TLSMode != TLSPlain {
		return nil, errors.New("SMTP TLS mode must be starttls, tls, or plain")
	}
	if config.Timeout < time.Second || config.Timeout > time.Minute {
		return nil, errors.New("SMTP timeout must be between 1s and 1m")
	}
	return &Sender{config: config, host: host, from: from}, nil
}

func (s *Sender) Send(ctx context.Context, recipient, subject, body string) error {
	to, err := mail.ParseAddress(strings.TrimSpace(recipient))
	if err != nil {
		return errors.New("notification recipient email is invalid")
	}
	if strings.TrimSpace(subject) == "" {
		return errors.New("notification email subject is required")
	}
	dialer := &net.Dialer{Timeout: s.config.Timeout}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: s.host}
	var connection net.Conn
	if s.config.TLSMode == TLSImplicit {
		connection, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", s.config.Address)
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", s.config.Address)
	}
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer connection.Close()
	deadline := time.Now().Add(s.config.Timeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return err
	}
	client, err := smtp.NewClient(connection, s.host)
	if err != nil {
		return fmt.Errorf("initialize SMTP client: %w", err)
	}
	defer client.Close()
	if s.config.TLSMode == TLSStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not advertise required STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("start SMTP TLS: %w", err)
		}
	}
	if s.config.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.config.Username, s.config.Password, s.host)); err != nil {
			return fmt.Errorf("authenticate to SMTP server: %w", err)
		}
	}
	if err := client.Mail(s.from.Address); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("open SMTP message: %w", err)
	}
	message, err := buildMessage(s.from, to, subject, body, time.Now().UTC())
	if err != nil {
		_ = writer.Close()
		return err
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("finish SMTP session: %w", err)
	}
	return nil
}

func buildMessage(from, to *mail.Address, subject, body string, sentAt time.Time) ([]byte, error) {
	if strings.ContainsAny(subject, "\r\n") {
		return nil, errors.New("notification email subject contains a line break")
	}
	var encoded bytes.Buffer
	quoted := quotedprintable.NewWriter(&encoded)
	if _, err := quoted.Write([]byte(body)); err != nil {
		return nil, err
	}
	if err := quoted.Close(); err != nil {
		return nil, err
	}
	var message bytes.Buffer
	fmt.Fprintf(&message, "From: %s\r\n", from.String())
	fmt.Fprintf(&message, "To: %s\r\n", to.String())
	fmt.Fprintf(&message, "Date: %s\r\n", sentAt.Format(time.RFC1123Z))
	fmt.Fprintf(&message, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	message.WriteString("MIME-Version: 1.0\r\n")
	message.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	message.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	message.Write(encoded.Bytes())
	message.WriteString("\r\n")
	return message.Bytes(), nil
}
