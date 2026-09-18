package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"time"
)

func sendMail(conn smtpConn, to, subject, body string) error {
	mime := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"
	header := "From: RSS Downloader<rss-dl@nikonor.ru>\nTo: " + to + "\nSubject: " + subject + "\n"
	msg := []byte(header + mime + body)

	tlsconfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         conn.Host,
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	serverConn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(conn.Host, conn.Port), tlsconfig)
	if err != nil {
		return fmt.Errorf("dial smtp %s:%s: %w", conn.Host, conn.Port, err)
	}
	defer serverConn.Close()

	if err := serverConn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return fmt.Errorf("set smtp deadline: %w", err)
	}

	c, err := smtp.NewClient(serverConn, conn.Host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}

	auth := smtp.PlainAuth("", conn.Login, conn.Password, conn.Host)
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}

	if err := c.Mail(conn.Login); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}

	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}

	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write message: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("close message: %w", err)
	}

	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}

	return nil
}
