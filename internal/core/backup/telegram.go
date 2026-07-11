package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// telegram is a tiny standalone Bot API client for off-site backup delivery. It
// is intentionally separate from internal/core/bot (that client is the two-way
// management bot); here we only ever push documents + a manifest message to one
// chat, so a self-contained sender keeps the dependency direction clean.
type telegram struct {
	token   string
	chatID  string
	baseURL string // defaults to https://api.telegram.org
	http    *http.Client
}

func newTelegram(token, chatID, baseURL string, hc *http.Client) *telegram {
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Minute}
	}
	return &telegram{token: token, chatID: chatID, baseURL: strings.TrimRight(baseURL, "/"), http: hc}
}

// sendDocument uploads one file (a single ciphertext part) via multipart.
func (t *telegram) sendDocument(ctx context.Context, filename string, body []byte, caption string) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("chat_id", t.chatID)
	if caption != "" {
		_ = mw.WriteField("caption", caption)
	}
	fw, err := mw.CreateFormFile("document", filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(body); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/bot%s/sendDocument", t.baseURL, t.token), &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return t.do(req, "sendDocument")
}

// sendMessage posts the manifest text so reassembly is verifiable from the chat.
func (t *telegram) sendMessage(ctx context.Context, text string) error {
	form := url.Values{}
	form.Set("chat_id", t.chatID)
	form.Set("text", text)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/bot%s/sendMessage", t.baseURL, t.token),
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return t.do(req, "sendMessage")
}

func (t *telegram) do(req *http.Request, method string) error {
	resp, err := t.http.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if resp.StatusCode != http.StatusOK {
		// Surface Telegram's "description" when present; otherwise the status.
		var apiErr struct {
			Description string `json:"description"`
		}
		_ = json.Unmarshal(raw, &apiErr)
		if apiErr.Description != "" {
			return fmt.Errorf("telegram %s: %s", method, apiErr.Description)
		}
		return fmt.Errorf("telegram %s: HTTP %d", method, resp.StatusCode)
	}
	return nil
}
