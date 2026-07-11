package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// client is a tiny Telegram Bot API client: just the long-poll getUpdates and
// sendMessage calls the management bot needs. No external dependency — the
// project deliberately keeps its module graph small. baseURL is injectable so
// tests can point at an httptest server.
type client struct {
	token   string
	baseURL string
	http    *http.Client
}

func newClient(token, baseURL string) *client {
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	return &client{token: token, baseURL: strings.TrimRight(baseURL, "/"), http: &http.Client{Timeout: 65 * time.Second}}
}

// The Telegram update envelope, as named types (the subset the bot reads).
// LanguageCode drives bot localization; Chat.Type gates credential-revealing
// replies (/config) to private chats only.
type tgUser struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	LanguageCode string `json:"language_code"`
}

type tgChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"` // "private", "group", "supergroup", "channel"
}

type tgMessage struct {
	MessageID int64   `json:"message_id"`
	From      *tgUser `json:"from"`
	Chat      *tgChat `json:"chat"`
	Text      string  `json:"text"`
}

// tgCallback is an inline-keyboard button tap. Data carries the routing
// token; Message identifies the message to edit in place.
type tgCallback struct {
	ID      string     `json:"id"`
	From    *tgUser    `json:"from"`
	Message *tgMessage `json:"message"`
	Data    string     `json:"data"`
}

type update struct {
	UpdateID      int64       `json:"update_id"`
	Message       *tgMessage  `json:"message"`
	CallbackQuery *tgCallback `json:"callback_query"`
}

func (c *client) method(name string) string {
	return fmt.Sprintf("%s/bot%s/%s", c.baseURL, c.token, name)
}

// getUpdates long-polls for new updates after offset (timeout in seconds).
func (c *client) getUpdates(ctx context.Context, offset int64, timeout int) ([]update, error) {
	q := url.Values{}
	q.Set("offset", strconv.FormatInt(offset, 10))
	q.Set("timeout", strconv.Itoa(timeout))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.method("getUpdates")+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		OK     bool     `json:"ok"`
		Result []update `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("telegram getUpdates not ok")
	}
	return out.Result, nil
}

// sendMessage posts a text reply to a chat. replyMarkup, when non-empty, is a
// JSON reply_markup value (e.g. the persistent menu keyboard).
func (c *client) sendMessage(ctx context.Context, chatID int64, text, replyMarkup string) error {
	q := url.Values{}
	q.Set("chat_id", strconv.FormatInt(chatID, 10))
	q.Set("text", text)
	if replyMarkup != "" {
		q.Set("reply_markup", replyMarkup)
	}
	return c.post(ctx, "sendMessage", q)
}

// editMessageText replaces a message's text + inline keyboard in place, so a
// tapped menu button updates the same message instead of spamming new ones.
func (c *client) editMessageText(ctx context.Context, chatID, messageID int64, text, replyMarkup string) error {
	q := url.Values{}
	q.Set("chat_id", strconv.FormatInt(chatID, 10))
	q.Set("message_id", strconv.FormatInt(messageID, 10))
	q.Set("text", text)
	if replyMarkup != "" {
		q.Set("reply_markup", replyMarkup)
	}
	return c.post(ctx, "editMessageText", q)
}

// answerCallbackQuery acknowledges a button tap so Telegram stops the client's
// loading spinner. An empty text just dismisses it; a non-empty text shows a
// toast, or a modal alert when alert is true.
func (c *client) answerCallbackQuery(ctx context.Context, id, text string, alert bool) error {
	q := url.Values{}
	q.Set("callback_query_id", id)
	if text != "" {
		q.Set("text", text)
	}
	if alert {
		q.Set("show_alert", "true")
	}
	return c.post(ctx, "answerCallbackQuery", q)
}

// sendPhoto uploads an image (PNG bytes) to a chat with an optional caption. Used
// by the config export's "QR" button, which renders the deep link as a scannable
// image on the bot host (the same secret already travels through Telegram as the
// deep-link text, so uploading its QR is no wider an exposure).
func (c *client) sendPhoto(ctx context.Context, chatID int64, filename string, photo []byte, caption string) error {
	return c.postMultipart(ctx, "sendPhoto", "photo", filename, photo, map[string]string{
		"chat_id": strconv.FormatInt(chatID, 10),
		"caption": caption,
	})
}

// sendDocument uploads a file (the client .toml) to a chat with an optional caption,
// so the operator can hand the config over as an importable file rather than pasted
// text.
func (c *client) sendDocument(ctx context.Context, chatID int64, filename string, doc []byte, caption string) error {
	return c.postMultipart(ctx, "sendDocument", "document", filename, doc, map[string]string{
		"chat_id": strconv.FormatInt(chatID, 10),
		"caption": caption,
	})
}

// postMultipart sends a multipart/form-data POST carrying one uploaded file plus
// the given text fields, and checks the HTTP status.
func (c *client) postMultipart(ctx context.Context, method, fileField, filename string, data []byte, fields map[string]string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if v != "" {
			_ = w.WriteField(k, v)
		}
	}
	fw, err := w.CreateFormFile(fileField, filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.method(method), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("telegram %s: HTTP %d", method, resp.StatusCode)
	}
	return nil
}

// setMyCommands registers the bot's slash-command menu (the "/" button in the
// Telegram UI). commandsJSON is a JSON array of {command, description} objects.
func (c *client) setMyCommands(ctx context.Context, commandsJSON string) error {
	q := url.Values{}
	q.Set("commands", commandsJSON)
	return c.post(ctx, "setMyCommands", q)
}

// post sends a form-encoded POST to a Bot API method and checks the status.
func (c *client) post(ctx context.Context, method string, q url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.method(method), strings.NewReader(q.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("telegram %s: HTTP %d", method, resp.StatusCode)
	}
	return nil
}
