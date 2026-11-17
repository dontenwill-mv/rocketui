package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// rcTime parses both REST ISO strings and DDP {"$date": millis}.
type rcTime struct{ time.Time }

func (t *rcTime) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" {
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		p, err := time.Parse(time.RFC3339, str)
		if err != nil {
			return err
		}
		t.Time = p
		return nil
	}
	var d struct {
		Date int64 `json:"$date"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return err
	}
	t.Time = time.UnixMilli(d.Date)
	return nil
}

type Room struct {
	ID      string `json:"rid"`
	Name    string `json:"name"`
	FName   string `json:"fname"`
	Type    string `json:"t"`
	Unread  int    `json:"unread"`
	Alert   bool   `json:"alert"`
	Open    bool   `json:"open"`
	Updated rcTime `json:"_updatedAt"`
	LastMsg rcTime `json:"lm"`
	// Uids ist bei DMs gesetzt und unterscheidet 1:1 von Mehrpersonen-DMs.
	Uids []string `json:"uids"`
}

// Multi reports whether more than two people can post here.
func (r Room) Multi() bool {
	if r.Type == "d" {
		return len(r.Uids) > 2
	}
	return true
}

func (r Room) Title() string {
	if r.FName != "" {
		return r.FName
	}
	return r.Name
}

func (r Room) Icon() string {
	switch r.Type {
	case "d":
		return "@"
	case "p":
		return "\U0001F512"
	default:
		return "#"
	}
}

type Attachment struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	TitleLink   string `json:"title_link"`
	ImageURL    string `json:"image_url"`
}

// Link returns the attachment path worth opening, if any.
// ponytail: title_link zuerst -- RC steckt in image_url nur das 480er-Thumbnail.
func (a Attachment) Link() string {
	if a.TitleLink != "" {
		return a.TitleLink
	}
	return a.ImageURL
}

// blockText accepts both "text": "x" and "text": {"type": "...", "text": "x"}.
type blockText struct{ Text string }

func (t *blockText) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &t.Text)
	}
	// Buttons verschachteln noch eine Ebene: {"text": {"type": .., "text": "x"}}.
	var o struct {
		Text *blockText `json:"text"`
	}
	json.Unmarshal(b, &o) // ponytail: unbekannte Form bleibt leer statt die Nachricht zu versenken
	if o.Text != nil {
		t.Text = o.Text.Text
	}
	return nil
}

// Block is one UIKit block of an app message. Umfragen der Poll-App bestehen
// aus section (Option plus Vote-Button) und context (Balken, Wählerliste).
type Block struct {
	Type      string `json:"type"`
	BlockID   string `json:"blockId"`
	AppID     string `json:"appId"`
	Text      blockText
	Accessory struct {
		Type     string `json:"type"`
		ActionID string `json:"actionId"`
		Value    string `json:"value"`
	} `json:"accessory"`
	Elements []blockText `json:"elements"`
}

type Reaction struct {
	Usernames []string `json:"usernames"`
}

type Message struct {
	ID   string `json:"_id"`
	RID  string `json:"rid"`
	Msg  string `json:"msg"`
	TS   rcTime `json:"ts"`
	TMID string `json:"tmid"`
	// T marks system messages (join, leave, topic change, ...).
	T         string              `json:"t"`
	Reactions map[string]Reaction `json:"reactions"`
	U         struct {
		ID       string `json:"_id"`
		Username string `json:"username"`
		Name     string `json:"name"`
	} `json:"u"`
	Attachments []Attachment `json:"attachments"`
	Replies     int          `json:"tcount"`
	EditedAt    rcTime       `json:"editedAt"`
	Blocks      []Block      `json:"blocks"`
}

// PollOption is one votable entry of an app message.
type PollOption struct {
	Label    string
	AppID    string
	BlockID  string
	ActionID string
	Value    string
}

// Poll returns the votable options of a message, empty for ordinary messages.
func (m Message) Poll() []PollOption {
	var out []PollOption
	for _, b := range m.Blocks {
		if b.Accessory.Type != "button" || b.Accessory.ActionID == "" {
			continue
		}
		out = append(out, PollOption{
			Label: b.Text.Text, AppID: b.AppID, BlockID: b.BlockID,
			ActionID: b.Accessory.ActionID, Value: b.Accessory.Value,
		})
	}
	return out
}

func (m Message) Author() string {
	if m.U.Name != "" {
		return m.U.Name
	}
	return m.U.Username
}

type Client struct {
	Base   string
	UserID string
	Token  string
	hc     *http.Client
}

func NewClient(cfg Config) *Client {
	tr := &http.Transport{}
	if cfg.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // ponytail: opt-in via config, self-signed servers exist
	}
	return &Client{
		Base:   strings.TrimRight(cfg.URL, "/"),
		UserID: cfg.UserID,
		Token:  cfg.Token,
		hc:     &http.Client{Timeout: 20 * time.Second, Transport: tr},
	}
}

func (c *Client) do(method, path string, q url.Values, body any, out any) error {
	return c.doPath(method, "/api/v1/"+path, q, body, out)
}

func (c *Client) doPath(method, path string, q url.Values, body any, out any) error {
	u := c.Base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", c.Token)
	req.Header.Set("X-User-Id", c.UserID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		json.Unmarshal(raw, &e)
		msg := e.Error
		if msg == "" {
			msg = e.Message
		}
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("%s: %s", path, msg)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) get(path string, q url.Values, out any) error {
	return c.do("GET", path, q, nil, out)
}

// Subscriptions returns every room the user follows, newest activity first.
func (c *Client) Subscriptions() ([]Room, error) {
	var res struct {
		Update []Room `json:"update"`
	}
	if err := c.get("subscriptions.get", nil, &res); err != nil {
		return nil, err
	}
	rooms := make([]Room, 0, len(res.Update))
	for _, r := range res.Update {
		if !r.Open || r.Type == "l" {
			continue
		}
		rooms = append(rooms, r)
	}
	sort.SliceStable(rooms, func(i, j int) bool {
		return roomStamp(rooms[i]).After(roomStamp(rooms[j]))
	})
	return rooms, nil
}

func roomStamp(r Room) time.Time {
	if r.LastMsg.After(r.Updated.Time) {
		return r.LastMsg.Time
	}
	return r.Updated.Time
}

func historyPath(t string) string {
	switch t {
	case "d":
		return "im.history"
	case "p":
		return "groups.history"
	default:
		return "channels.history"
	}
}

// History returns up to n messages of a room, oldest first. A non-zero before
// limits the window to messages older than that timestamp.
func (c *Client) History(r Room, n int, before time.Time) ([]Message, error) {
	q := url.Values{"roomId": {r.ID}, "count": {strconv.Itoa(n)}}
	if !before.IsZero() {
		q.Set("latest", before.UTC().Format(time.RFC3339Nano))
	}
	var res struct {
		Messages []Message `json:"messages"`
	}
	if err := c.get(historyPath(r.Type), q, &res); err != nil {
		return nil, err
	}
	for i, j := 0, len(res.Messages)-1; i < j; i, j = i+1, j-1 {
		res.Messages[i], res.Messages[j] = res.Messages[j], res.Messages[i]
	}
	return res.Messages, nil
}

type SpotlightUser struct {
	ID       string `json:"_id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Status   string `json:"status"`
}

type SpotlightRoom struct {
	ID   string `json:"_id"`
	Name string `json:"name"`
	Type string `json:"t"`
}

// Spotlight searches users and rooms server-wide, also outside own subscriptions.
func (c *Client) Spotlight(query string) ([]SpotlightUser, []SpotlightRoom, error) {
	var res struct {
		Users []SpotlightUser `json:"users"`
		Rooms []SpotlightRoom `json:"rooms"`
	}
	err := c.get("spotlight", url.Values{"query": {query}}, &res)
	return res.Users, res.Rooms, err
}

// SearchMessages searches inside one room. Rocket.Chat has no cross-room REST search.
func (c *Client) SearchMessages(roomID, text string, n int) ([]Message, error) {
	q := url.Values{"roomId": {roomID}, "searchText": {text}, "count": {strconv.Itoa(n)}}
	var res struct {
		Messages []Message `json:"messages"`
	}
	err := c.get("chat.search", q, &res)
	return res.Messages, err
}

// OpenDM creates (or returns) the direct room with a user.
func (c *Client) OpenDM(username string) (Room, error) {
	var res struct {
		Room struct {
			ID string `json:"_id"`
			T  string `json:"t"`
		} `json:"room"`
	}
	err := c.do("POST", "im.create", nil, map[string]string{"username": username}, &res)
	return Room{ID: res.Room.ID, Type: "d", Name: username, FName: username}, err
}

// Me returns the authenticated user (needed for own-reaction toggling).
func (c *Client) Me() (id, username, name string, err error) {
	var res struct {
		ID       string `json:"_id"`
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	err = c.get("me", nil, &res)
	return res.ID, res.Username, res.Name, err
}

// PostMessage sends text to a room; tmid replies inside a thread when set.
func (c *Client) PostMessage(roomID, text, tmid string) (Message, error) {
	body := map[string]string{"roomId": roomID, "text": text}
	if tmid != "" {
		body["tmid"] = tmid
	}
	var res struct {
		Message Message `json:"message"`
	}
	err := c.do("POST", "chat.postMessage", nil, body, &res)
	return res.Message, err
}

// UpdateMessage rewrites one of your own messages. Der Server lehnt fremde
// Nachrichten ab, ebenso abgelaufene Bearbeitungsfenster.
func (c *Client) UpdateMessage(roomID, msgID, text string) (Message, error) {
	var res struct {
		Message Message `json:"message"`
	}
	err := c.do("POST", "chat.update", nil, map[string]string{
		"roomId": roomID, "msgId": msgID, "text": text,
	}, &res)
	return res.Message, err
}

// Vote presses one UIKit button of an app message. Die App aktualisiert die
// Nachricht danach selbst, die neue Fassung kommt über DDP zurück.
func (c *Client) Vote(o PollOption, msgID, roomID string) error {
	return c.doPath("POST", "/api/apps/ui.interaction/"+o.AppID, nil, map[string]any{
		"type":      "blockAction",
		"actionId":  o.ActionID,
		"payload":   map[string]string{"blockId": o.BlockID, "value": o.Value},
		"container": map[string]string{"type": "message", "id": msgID},
		"mid":       msgID,
		"rid":       roomID,
		"triggerId": triggerID(),
	}, nil)
}

// triggerID mimics Meteor's Random.id(), which the web client sends along.
func triggerID() string {
	b := make([]byte, 9)
	rand.Read(b)
	return hex.EncodeToString(b)[:17]
}

// React toggles one emoji reaction on a message. emoji must include the colons.
func (c *Client) React(messageID, emoji string, react bool) error {
	return c.do("POST", "chat.react", nil, map[string]any{
		"messageId": messageID, "emoji": emoji, "shouldReact": react,
	}, nil)
}

// ThreadMessages returns the replies of one thread, oldest first.
func (c *Client) ThreadMessages(tmid string, n int) ([]Message, error) {
	q := url.Values{"tmid": {tmid}, "count": {strconv.Itoa(n)}}
	var res struct {
		Messages []Message `json:"messages"`
	}
	err := c.get("chat.getThreadMessages", q, &res)
	return res.Messages, err
}

// Download fetches an authenticated upload path into a temp file and returns its name.
func (c *Client) Download(path string) (string, error) {
	u := path
	if strings.HasPrefix(path, "/") {
		u = c.Base + path
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Auth-Token", c.Token)
	req.Header.Set("X-User-Id", c.UserID)
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("download %s: %s", path, resp.Status)
	}
	name := path
	if i := strings.IndexByte(name, '?'); i >= 0 {
		name = name[:i]
	}
	f, err := os.CreateTemp("", "rc-tui-*"+filepath.Ext(name))
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return f.Name(), nil
}
