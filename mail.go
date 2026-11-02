package main

import (
	"fmt"
	"html"
	"net/smtp"
	"strings"
	"time"
)

type mailConfig struct {
	host     string
	port     string
	username string
	password string
	from     string
}

func loadMailConfig() mailConfig {
	return mailConfig{
		host:     strings.TrimSpace(envOr("SMTP_HOST", "")),
		port:     strings.TrimSpace(envOr("SMTP_PORT", "587")),
		username: strings.TrimSpace(envOr("SMTP_USERNAME", "")),
		password: strings.TrimSpace(envOr("SMTP_PASSWORD", "")),
		from:     strings.TrimSpace(envOr("MAIL_FROM", "info@in2ai.biz")),
	}
}

func (m mailConfig) configured() bool {
	return m.host != "" && m.username != "" && m.password != "" && strings.Contains(m.from, "@")
}

func verificationEmail(name, link string) (subject, textBody, htmlBody string) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "there"
	}
	subject = "Verify your email for In2AI"
	textBody = strings.Join([]string{
		"Hello " + name + ",",
		"",
		"Please verify your email address using the link below to continue with In2AI. Once verified, an admin will review and approve your account.",
		"",
		"Verify Your Email",
		link,
		"",
		"This link expires in 30 minutes.",
		"",
		"If you didn't create this account, please let us know. We'll promptly delete it to protect your information.",
		"",
		"Best regards,",
		"In2AI Support Team",
		"info@in2ai.biz",
		"",
		"In2AI",
	}, "\n")

	safeName := html.EscapeString(name)
	safeLink := html.EscapeString(link)
	htmlBody = fmt.Sprintf(`<p>Hello %s,</p>
<p>Please verify your email address using the link below to continue with In2AI. Once verified, an admin will review and approve your account.</p>
<p><a href="%s">Verify Your Email</a></p>
<p>This link expires in 30 minutes.</p>
<p>If you didn't create this account, please let us know. We'll promptly delete it to protect your information.</p>
<p>Best regards,<br>In2AI Support Team<br>info@in2ai.biz</p>
<p>In2AI</p>`, safeName, safeLink)
	return subject, textBody, htmlBody
}

func (m mailConfig) send(to, subject, textBody, htmlBody string) error {
	to = strings.TrimSpace(to)
	if !emailPattern.MatchString(to) {
		return fmt.Errorf("refusing to send mail to %q", to)
	}
	from := m.from
	boundary := fmt.Sprintf("in2ai-%d", time.Now().UnixNano())
	message := strings.Join([]string{
		"From: In2AI Support Team <" + from + ">",
		"To: " + to,
		"Subject: " + cleanHeader(subject),
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=" + boundary,
		"",
		"--" + boundary,
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		textBody,
		"--" + boundary,
		"Content-Type: text/html; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		htmlBody,
		"--" + boundary + "--",
		"",
	}, "\r\n")

	auth := smtp.PlainAuth("", m.username, m.password, m.host)
	return smtp.SendMail(m.host+":"+m.port, auth, from, []string{to}, []byte(message))
}

func cleanHeader(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, value)
}
