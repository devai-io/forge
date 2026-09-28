// Package mail sends the one e-mail Forge ever sends: a password-reset link.
package mail

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/devai-io/forge/internal/config"
)

type Mailer struct {
	cfg config.SMTP
}

func New(cfg config.SMTP) *Mailer { return &Mailer{cfg: cfg} }

func (m *Mailer) Enabled() bool { return m.cfg.Enabled() }

// Send delivers a plain-text message over STARTTLS (port 587) or implicit
// TLS (465), authenticating with PLAIN (e.g. a Gmail app password).
func (m *Mailer) Send(to, subject, body string) error {
	if !m.Enabled() {
		return fmt.Errorf("smtp is not configured")
	}
	addr := net.JoinHostPort(m.cfg.Host, fmt.Sprint(m.cfg.Port))
	msg := strings.Join([]string{
		"From: Forge <" + m.cfg.From + ">",
		"To: " + to,
		"Subject: " + subject,
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n")

	var c *smtp.Client
	var err error
	tlsCfg := &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}
	if m.cfg.Port == 465 {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, tlsCfg)
		if err != nil {
			return err
		}
		c, err = smtp.NewClient(conn, m.cfg.Host)
		if err != nil {
			return err
		}
	} else {
		conn, err := net.DialTimeout("tcp", addr, 15*time.Second)
		if err != nil {
			return err
		}
		c, err = smtp.NewClient(conn, m.cfg.Host)
		if err != nil {
			return err
		}
		if err = c.StartTLS(tlsCfg); err != nil {
			c.Close()
			return err
		}
	}
	defer c.Close()

	if m.cfg.User != "" {
		if err = c.Auth(smtp.PlainAuth("", m.cfg.User, m.cfg.Password, m.cfg.Host)); err != nil {
			return err
		}
	}
	if err = c.Mail(m.cfg.From); err != nil {
		return err
	}
	if err = c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write([]byte(msg)); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
