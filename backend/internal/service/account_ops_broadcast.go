package service

import (
	"context"
	"errors"
	"html"
	"strings"
	"time"
)

// OpsBroadcastMessage is a notification that is not tied to an account ops
// event episode (for example an upstream multiplier change). It is delivered
// once to the saved email recipient and every enabled Webhook channel.
type OpsBroadcastMessage struct {
	Title string
	// Account, Value and Reference fill the custom Webhook template variables
	// {{account}}, {{balance}} and {{threshold}}.
	Account    string
	Value      string
	Reference  string
	ObservedAt time.Time
	Fields     [][2]string
	// Warning selects the orange card colour; otherwise green.
	Warning bool
}

var errOpsBroadcastNoDestination = errors.New("account ops notifications are disabled or have no destination")

// BroadcastOpsNotification sends msg through the account ops channels. It
// honours the account ops master switch and the shared per-robot rate slot,
// and returns how many destinations accepted the message.
func (s *AccountOpsService) BroadcastOpsNotification(ctx context.Context, msg OpsBroadcastMessage) (int, error) {
	if s == nil {
		return 0, errOpsBroadcastNoDestination
	}
	query, cancel := context.WithTimeout(ctx, 5*time.Second)
	s.settingsMu.Lock()
	c, err := s.loadConfig(query)
	s.settingsMu.Unlock()
	cancel()
	if err != nil {
		return 0, err
	}
	if !c.Enabled || !c.hasDestination() {
		return 0, errOpsBroadcastNoDestination
	}
	message := s.opsBroadcastMessage(msg)
	delivered := 0
	var lastErr error
	if c.Recipient != "" && s.email != nil {
		send, stop := context.WithTimeout(ctx, 15*time.Second)
		if err := s.email.SendEmail(send, c.Recipient, message.title, message.html); err != nil {
			lastErr = err
		} else {
			delivered++
		}
		stop()
	}
	for _, w := range c.Webhooks {
		if !w.Enabled {
			continue
		}
		send, stop := context.WithTimeout(ctx, 15*time.Second)
		if rate, ok := s.repo.(interface {
			ReserveRobotTest(context.Context, string) error
		}); ok {
			identity, identityErr := s.robotIdentity(w)
			if identityErr != nil {
				stop()
				lastErr = identityErr
				continue
			}
			if err := rate.ReserveRobotTest(send, identity); err != nil {
				stop()
				lastErr = errors.New("robot rate reservation failed")
				continue
			}
		}
		if err := s.sendRobot(send, w, message); err != nil {
			lastErr = err
		} else {
			delivered++
		}
		stop()
	}
	if delivered == 0 && lastErr != nil {
		return 0, lastErr
	}
	return delivered, nil
}

func (s *AccountOpsService) opsBroadcastMessage(msg OpsBroadcastMessage) accountOpsMessage {
	observed := msg.ObservedAt
	if observed.IsZero() {
		observed = time.Now()
	}
	tz := s.timezone
	if tz == nil {
		tz = time.UTC
	}
	observedText := observed.In(tz).Format("2006-01-02 15:04:05")
	fields := append([][2]string{}, msg.Fields...)
	fields = append(fields, [2]string{"时间", observedText})
	plain := make([]string, 0, len(fields))
	markdown := "### " + msg.Title
	body := "<h2>" + html.EscapeString(msg.Title) + "</h2><table style=\"border-collapse:collapse\">"
	for _, f := range fields {
		plain = append(plain, f[0]+"："+f[1])
		markdown += "\n**" + f[0] + "**：" + escapeOpsMarkdown(f[1])
		body += "<tr><th style=\"text-align:left;padding:8px\">" + html.EscapeString(f[0]) + "</th><td style=\"padding:8px\">" + html.EscapeString(f[1]) + "</td></tr>"
	}
	body += "</table>"
	color := "green"
	if msg.Warning {
		color = "orange"
	}
	return accountOpsMessage{
		title: msg.Title, robotTitle: msg.Title, plain: strings.Join(plain, "\n"), markdown: markdown, html: body,
		color: color, account: msg.Account, balance: msg.Value, threshold: msg.Reference, observedTime: observedText,
	}
}

// HasBalanceRule reports whether account ops already has an enabled balance
// threshold rule for the account, so global upstream alerts do not duplicate it.
func (s *AccountOpsService) HasBalanceRule(ctx context.Context, accountID int64) bool {
	if s == nil {
		return false
	}
	query, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s.settingsMu.Lock()
	c, err := s.loadConfig(query)
	s.settingsMu.Unlock()
	if err != nil {
		return false
	}
	for _, rule := range c.BalanceThresholds {
		if rule.AccountID == accountID && rule.Enabled {
			return true
		}
	}
	return false
}
