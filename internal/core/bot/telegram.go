package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// client is a tiny Telegram Bot API client: just the long-poll getUpdates and
// sendMessage calls the management bot needs. No external dependency, since the
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
	// ViaBot marks a message the user sent by picking one of this bot's inline
	// results. It arrives looking like something they typed, and reading it as
	// an answer to whatever form is open is exactly the mistake that would let a
	// catalogue entry be added by the text of any message that happened to look
	// like one.
	ViaBot *tgUser `json:"via_bot"`
}

// tgInlineQuery is the operator typing in the inline box the catalogue button
// opened. Offset is the next_offset handed back earlier, which lets a long catalogue be
// paged by scrolling.
type tgInlineQuery struct {
	ID     string  `json:"id"`
	From   *tgUser `json:"from"`
	Query  string  `json:"query"`
	Offset string  `json:"offset"`
}

// tgChosenInlineResult is which entry they picked. Telegram only sends it when
// inline feedback is switched on for the bot (BotFather → /setinlinefeedback,
// "Enabled"); without it the pick still puts a message in the chat, but the bot
// is never told it happened, and a tag must never be added from the text of a
// message, only from this event.
type tgChosenInlineResult struct {
	ResultID string  `json:"result_id"`
	From     *tgUser `json:"from"`
	Query    string  `json:"query"`
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
	UpdateID           int64                 `json:"update_id"`
	Message            *tgMessage            `json:"message"`
	CallbackQuery      *tgCallback           `json:"callback_query"`
	InlineQuery        *tgInlineQuery        `json:"inline_query"`
	ChosenInlineResult *tgChosenInlineResult `json:"chosen_inline_result"`
}

// do sends the request with the token kept out of anything that comes back.
// A transport failure arrives as *url.Error carrying the full request URL, and
// the URL embeds the bot token. Logging such an error verbatim writes a live
// credential into the journal, readable by everyone in adm and by every log
// shipper. The error keeps its type and its Timeout()/Temporary() answers; only
// the address is rewritten.
func (c *client) do(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err == nil {
		return resp, nil
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		ue.URL = c.hideToken(ue.URL)
		return nil, ue
	}
	return nil, errors.New(c.hideToken(err.Error()))
}

// hideToken replaces the token wherever it appears in a string.
func (c *client) hideToken(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "…")
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
	resp, err := c.do(req)
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

// editMessageText replaces a message's text + inline keyboard in place, leaving a
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
// deep-link text, and uploading its QR is no wider an exposure).
func (c *client) sendPhoto(ctx context.Context, chatID int64, filename string, photo []byte, caption string) error {
	return c.postMultipart(ctx, "sendPhoto", "photo", filename, photo, map[string]string{
		"chat_id": strconv.FormatInt(chatID, 10),
		"caption": caption,
	})
}

// sendDocument uploads a file (the client .toml) to a chat with an optional caption,
// so the operator can hand the config over as an importable file instead of pasted
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
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("telegram %s: HTTP %d", method, resp.StatusCode)
	}
	return nil
}

// inlineArticle is one entry of an inline answer: what the list shows, and the
// message picking it puts in the chat.
type inlineArticle struct {
	ID          string `json:"id"`
	Type        string `json:"type"` // always "article"
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Content     struct {
		Text string `json:"message_text"`
	} `json:"input_message_content"`
}

// answerInlineQuery returns one page of results. They are personal (the answer
// depends on which draft the asker has open) and not cached. Telegram asks
// again instead of showing another operator's page.
func (c *client) answerInlineQuery(ctx context.Context, id string, results []inlineArticle, nextOffset string) error {
	body, err := json.Marshal(results)
	if err != nil {
		return err
	}
	q := url.Values{}
	q.Set("inline_query_id", id)
	q.Set("results", string(body))
	q.Set("cache_time", "0")
	q.Set("is_personal", "true")
	if nextOffset != "" {
		q.Set("next_offset", nextOffset)
	}
	return c.post(ctx, "answerInlineQuery", q)
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
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("telegram %s: HTTP %d", method, resp.StatusCode)
	}
	return nil
}
