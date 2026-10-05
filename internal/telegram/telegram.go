// Package telegram is the small part of the Telegram Bot API Forge uses:
// long-polling for updates (so the server needs no public webhook), sending
// and editing messages with inline buttons, and answering button presses.
//
// The bot token is part of every request URL; errors never repeat the URL,
// so the token cannot end up in a log.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.telegram.org"

// MaxText is how much text one message holds (Telegram's limit is 4096
// characters; this leaves room for markup).
const MaxText = 3800

var ErrNoToken = errors.New("no Telegram bot token in the vault (tag integration:telegram)")

type Client struct {
	// BaseURL is the Bot API origin (a test server in tests).
	BaseURL string
	token   func(context.Context) (string, error)
	http    *http.Client
}

func New(token func(context.Context) (string, error)) *Client {
	return &Client{BaseURL: DefaultBaseURL, token: token, http: &http.Client{}}
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type Message struct {
	MessageID int64    `json:"message_id"`
	From      *User    `json:"from"`
	Chat      Chat     `json:"chat"`
	Text      string   `json:"text"`
	Caption   string   `json:"caption"`
	ReplyTo   *Message `json:"reply_to_message"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Button is an inline keyboard button: Data comes back as a callback, URL
// opens a link.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// Keyboard is rows of buttons.
type Keyboard [][]Button

// APIError is Telegram's answer to a request it refused.
type APIError struct {
	Method      string
	Code        int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// call posts params as JSON to the method and decodes its result.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	if token == "" {
		return ErrNoToken
	}
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/bot"+token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: bad request", method)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // its message holds the URL, and so the token
			err = ue.Err
		}
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("telegram %s: HTTP %d", method, resp.StatusCode)
	}
	if !env.OK {
		return &APIError{Method: method, Code: env.ErrorCode, Description: env.Description}
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// GetMe is the bot itself (checks the token).
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	return &u, c.call(ctx, "getMe", struct{}{}, &u)
}

// GetUpdates long-polls for up to wait (Telegram holds the request open).
func (c *Client) GetUpdates(ctx context.Context, offset int64, wait time.Duration) ([]Update, error) {
	ctx, cancel := context.WithTimeout(ctx, wait+15*time.Second)
	defer cancel()
	var ups []Update
	err := c.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": int(wait.Seconds()),
		"allowed_updates": []string{"message", "callback_query"}}, &ups)
	return ups, err
}

// Send says something in a chat.
type Send struct {
	ChatID   int64
	Text     string
	HTML     bool // Text is Telegram HTML (see HTML / Escape)
	Keyboard Keyboard
	ReplyTo  int64
}

func (c *Client) Send(ctx context.Context, m Send) (*Message, error) {
	p := map[string]any{"chat_id": m.ChatID, "text": m.Text, "link_preview_options": map[string]bool{"is_disabled": true}}
	if m.HTML {
		p["parse_mode"] = "HTML"
	}
	if len(m.Keyboard) > 0 {
		p["reply_markup"] = map[string]any{"inline_keyboard": m.Keyboard}
	}
	if m.ReplyTo != 0 {
		p["reply_parameters"] = map[string]any{"message_id": m.ReplyTo, "allow_sending_without_reply": true}
	}
	var out Message
	err := c.call(ctx, "sendMessage", p, &out)
	var ae *APIError
	if m.HTML && errors.As(err, &ae) && strings.Contains(ae.Description, "parse entities") {
		// Markup Telegram does not accept: say it as plain text instead.
		p["text"], p["parse_mode"] = StripTags(m.Text), nil
		delete(p, "parse_mode")
		err = c.call(ctx, "sendMessage", p, &out)
	}
	return &out, err
}

// Edit replaces a message's text and buttons (nil keyboard removes them).
func (c *Client) Edit(ctx context.Context, chatID, messageID int64, text string, html bool, kb Keyboard) error {
	p := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text,
		"link_preview_options": map[string]bool{"is_disabled": true}}
	if html {
		p["parse_mode"] = "HTML"
	}
	if kb == nil {
		kb = Keyboard{}
	}
	p["reply_markup"] = map[string]any{"inline_keyboard": kb}
	err := c.call(ctx, "editMessageText", p, nil)
	var ae *APIError
	if errors.As(err, &ae) && strings.Contains(ae.Description, "not modified") {
		return nil
	}
	return err
}

// AnswerCallback acknowledges a button press (text shows as a brief toast).
func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

// Typing shows "typing…" in the chat for a few seconds.
func (c *Client) Typing(ctx context.Context, chatID int64) error {
	return c.call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": "typing"}, nil)
}

type Command struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// SetCommands sets the menu of slash commands the chat offers.
func (c *Client) SetCommands(ctx context.Context, cmds []Command) error {
	return c.call(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil)
}
