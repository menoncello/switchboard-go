package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tierSwitchRetryAfter is how long after a failed tier switch email the next attempt waits.
// A var so tests can shrink it.
var tierSwitchRetryAfter = 5 * time.Minute

// SMTPNotifier sends the email alerts. The tier switch warning goes out once per extra
// tier per episode; an episode ends when tier 0 serves a request. After a failed send
// no new attempt happens until tierSwitchRetryAfter has passed.
type SMTPNotifier struct {
	cfg SMTPConfig
	now func() time.Time // injectable clock for the retry backoff
	mu  sync.Mutex
	// warned holds the tiers already warned about in the current episode, each with the
	// token of the send that put it there, so a failed send only undoes its own entry.
	warned   map[string]uint64
	nextSend uint64
	retryAt  time.Time // no tier switch send before this instant (zero: no backoff)
}

func NewSMTPNotifier(cfg SMTPConfig) *SMTPNotifier {
	return &SMTPNotifier{cfg: cfg, now: time.Now, warned: map[string]uint64{}}
}
func (n *SMTPNotifier) NotifySwitch(idx int, st StatusResponse) {
	go n.send("Switchboard Go switched upstream key", fmt.Sprintf("Switched away from key %d\n\n%+v", idx, st))
}
func (n *SMTPNotifier) NotifyAllExhausted(st StatusResponse) {
	go n.send("Switchboard Go exhausted all upstream keys", fmt.Sprintf("All keys exhausted\n\n%+v", st))
}
func (n *SMTPNotifier) NotifyPaidFirstUse(tier, model, day string) {
	go n.send("Switchboard Go paid tier in use: "+tier, fmt.Sprintf("Paid tier %q served its first request of %s (model %q).", tier, day, model))
}
func (n *SMTPNotifier) configured() bool {
	return n.cfg.Host != "" && n.cfg.To != "" && n.cfg.From != ""
}

// NotifyTierSwitch warns that an extra tier served a request tier 0 did not, once per
// extra tier per episode and not while a failed send is backing off.
func (n *SMTPNotifier) NotifyTierSwitch(tier, model string) {
	if !n.configured() {
		return
	}
	n.mu.Lock()
	if _, done := n.warned[tier]; done || n.now().Before(n.retryAt) {
		n.mu.Unlock()
		return
	}
	n.nextSend++
	token := n.nextSend
	n.warned[tier] = token
	n.mu.Unlock()
	go func() {
		if n.send("Switchboard Go switched tier: "+tier, fmt.Sprintf("Tier 0 did not serve a request; tier %q served it (model %q).", tier, truncateModel(model))) {
			return
		}
		// A lost warning must not silence the episode, but SMTP is probably down: retry
		// after the backoff, not on the next request. Only undo this send's own entry,
		// so a slow failure from an old episode cannot rearm a newer one.
		n.mu.Lock()
		if n.warned[tier] == token {
			delete(n.warned, tier)
		}
		n.retryAt = n.now().Add(tierSwitchRetryAfter)
		n.mu.Unlock()
	}()
}

// rearmTierSwitch ends the switch episode: tier 0 served a request. It does not clear
// the backoff after a failed send.
func (n *SMTPNotifier) rearmTierSwitch() {
	n.mu.Lock()
	clear(n.warned)
	n.mu.Unlock()
}

// send reports whether the email went out.
func (n *SMTPNotifier) send(subject, body string) bool {
	if !n.configured() {
		return false
	}
	addr := net.JoinHostPort(n.cfg.Host, strconv.Itoa(n.cfg.Port))
	auth := smtp.Auth(nil)
	if strings.TrimSpace(n.cfg.Username) != "" {
		auth = smtp.PlainAuth("", n.cfg.Username, n.cfg.Password, n.cfg.Host)
	}
	msg := []byte("To: " + n.cfg.To + "\r\nSubject: " + subject + "\r\n\r\n" + body + "\r\n")
	if err := sendMail(addr, auth, n.cfg.From, []string{n.cfg.To}, msg, n.cfg.TLS, n.cfg.StartTLS); err != nil {
		log.Printf("smtp notification failed: %v", err)
		return false
	}
	return true
}

// tiny indirection to keep stdlib-only and testable.
var sendMail = func(addr string, auth smtp.Auth, from string, to []string, msg []byte, useTLS, useStartTLS bool) error {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	host, _, _ := net.SplitHostPort(addr)
	if useTLS {
		conn = tls.Client(conn, &tls.Config{ServerName: host})
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Quit()
	if useStartTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return err
			}
		}
	}
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}
