package main

import (
	"context"
	"fmt"
	"html"
	"log"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/resend/resend-go/v4"
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

const defaultCalendlyURL = "https://calendly.com/in2ai-info/30min"

type resendConfig struct {
	apiKey      string
	from        string
	calendlyURL string
}

func loadResendConfig() resendConfig {
	from := strings.TrimSpace(os.Getenv("RESEND_FROM"))
	if from == "" {
		from = "no-reply <" + strings.TrimSpace(envOr("MAIL_FROM", "info@in2ai.biz")) + ">"
	}
	return resendConfig{
		apiKey:      strings.TrimSpace(os.Getenv("RESEND_API_KEY")),
		from:        from,
		calendlyURL: bookingLink(os.Getenv("CALENDLY_URL")),
	}
}

func (c resendConfig) configured() bool {
	return c.apiKey != "" && strings.Contains(c.from, "@")
}

func bookingLink(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return defaultCalendlyURL
	}
	return parsed.String()
}

func applicationThankYouEmail(firstName, jobTitle, company, calendlyURL string) (subject, textBody, htmlBody string) {
	firstName = strings.TrimSpace(firstName)
	if firstName == "" {
		firstName = "there"
	}
	jobTitle = strings.TrimSpace(jobTitle)
	company = strings.TrimSpace(company)
	if company == "" {
		company = "In2AI"
	}
	calendlyURL = bookingLink(calendlyURL)

	subject = cleanHeader("Thank you for applying to " + jobTitle)
	textBody = strings.Join([]string{
		"Hi " + firstName + ",",
		"",
		"Thank you for your interest in the " + jobTitle + " position at " + company + "!",
		"",
		"We've received your application and appreciate the time you took to apply.",
		"",
		"We'd love to learn more about your background and experience. Please use the link below to schedule a brief introductory call at a time that works best for you.",
		"",
		"Schedule your call: " + calendlyURL,
		"",
		"Once you've selected a time, you'll receive a calendar invitation with a Google Meet link.",
		"",
		"We look forward to speaking with you!",
		"",
		"Best regards,",
		"In2AI Co-Founder",
		"Thomas Liu",
	}, "\n")

	safeName := html.EscapeString(firstName)
	safeTitle := html.EscapeString(jobTitle)
	safeCompany := html.EscapeString(company)
	safeLink := html.EscapeString(calendlyURL)
	htmlBody = fmt.Sprintf(`<p>Hi %s,</p>
<p>Thank you for your interest in the <strong>%s</strong> position at <strong>%s</strong>!</p>
<p>We've received your application and appreciate the time you took to apply.</p>
<p>We'd love to learn more about your background and experience. Please use the link below to schedule a brief introductory call at a time that works best for you.</p>
<p><strong>Schedule your call:</strong> <a href="%s">%s</a></p>
<p>Once you've selected a time, you'll receive a calendar invitation with a Google Meet link.</p>
<p>We look forward to speaking with you!</p>
<p>Best regards,<br>In2AI Co-Founder<br>Thomas Liu</p>`, safeName, safeTitle, safeCompany, safeLink, safeLink)
	return subject, textBody, htmlBody
}

func (c resendConfig) sendApplicationThankYou(to, firstName, jobTitle, company string) {
	to = strings.TrimSpace(to)
	if !c.configured() || !emailPattern.MatchString(to) {
		return
	}
	subject, textBody, htmlBody := applicationThankYouEmail(firstName, jobTitle, company, c.calendlyURL)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sent, err := resend.NewClient(c.apiKey).Emails.SendWithContext(ctx, &resend.SendEmailRequest{
		From:    c.from,
		To:      []string{to},
		Subject: subject,
		Html:    htmlBody,
		Text:    textBody,
	})
	if err != nil {
		log.Printf("application thank-you email failed for %s: %v", to, err)
		return
	}
	log.Printf("application thank-you email sent to %s: %s", to, sent.Id)
}

func cleanHeader(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, value)
}
