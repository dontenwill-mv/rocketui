package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

type ddpFrame struct {
	Msg        string `json:"msg"`
	ID         string `json:"id"`
	Collection string `json:"collection"`
	Fields     struct {
		EventName string            `json:"eventName"`
		Args      []json.RawMessage `json:"args"`
	} `json:"fields"`
	Error *struct {
		Message string `json:"message"`
		Reason  string `json:"reason"`
	} `json:"error"`
}

// events turns a DDP stream frame into UI messages.
func (f ddpFrame) events() []tea.Msg {
	if f.Msg != "changed" {
		return nil
	}
	var out []tea.Msg
	switch f.Collection {
	case "stream-room-messages":
		for _, a := range f.Fields.Args {
			var m Message
			if json.Unmarshal(a, &m) == nil && m.ID != "" {
				out = append(out, newMessageMsg{m})
			}
		}
	case "stream-notify-user":
		if strings.HasSuffix(f.Fields.EventName, "/subscriptions-changed") {
			for _, a := range f.Fields.Args {
				var r Room
				if json.Unmarshal(a, &r) == nil && r.ID != "" {
					out = append(out, subChangedMsg{r})
				}
			}
		}
	}
	return out
}

func wsURL(base string) string {
	u := strings.TrimRight(base, "/")
	u = strings.Replace(u, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1)
	return u + "/websocket"
}

// runDDP keeps a realtime connection alive and pushes events into out.
func runDDP(cfg Config, out chan<- tea.Msg) {
	for {
		err := ddpSession(cfg, out)
		msg := "verbindung verloren"
		if err != nil {
			msg = err.Error()
		}
		out <- ddpStatusMsg{Connected: false, Err: msg}
		time.Sleep(3 * time.Second)
	}
}

func ddpSession(cfg Config, out chan<- tea.Msg) error {
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	if cfg.Insecure {
		d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	c, _, err := d.Dial(wsURL(cfg.URL), nil)
	if err != nil {
		return err
	}
	defer c.Close()

	send := func(v any) error { return c.WriteJSON(v) }
	if err := send(map[string]any{"msg": "connect", "version": "1", "support": []string{"1"}}); err != nil {
		return err
	}

	// ponytail: server pings us regularly, so a read deadline + pong is enough — no own ping writer.
	for {
		c.SetReadDeadline(time.Now().Add(90 * time.Second))
		_, raw, err := c.ReadMessage()
		if err != nil {
			return err
		}
		var f ddpFrame
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		switch f.Msg {
		case "ping":
			if err := send(map[string]any{"msg": "pong"}); err != nil {
				return err
			}
		case "connected":
			err = send(map[string]any{"msg": "method", "method": "login", "id": "login",
				"params": []any{map[string]any{"resume": cfg.Token}}})
			if err != nil {
				return err
			}
		case "result":
			if f.ID != "login" {
				continue
			}
			if f.Error != nil {
				return fmt.Errorf("ddp-login: %s (token als resume-token untauglich?)", f.Error.Reason+f.Error.Message)
			}
			err = send(map[string]any{"msg": "sub", "id": "msgs", "name": "stream-room-messages",
				"params": []any{"__my_messages__", false}})
			if err != nil {
				return err
			}
			err = send(map[string]any{"msg": "sub", "id": "subs", "name": "stream-notify-user",
				"params": []any{cfg.UserID + "/subscriptions-changed", false}})
			if err != nil {
				return err
			}
			out <- ddpStatusMsg{Connected: true}
		default:
			for _, ev := range f.events() {
				out <- ev
			}
		}
	}
}
