package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/Berthelmaster/pi-sens/internal/alerts"
)

// userAgent is sent with every request.
const userAgent = "pisens"

var defaultClient = &http.Client{Timeout: RequestTimeout}

// post sends body to rawURL and turns a non-2xx response into a
// *StatusError.
func post(ctx context.Context, client *http.Client, rawURL string, header http.Header, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // so the connection can be reused
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(snippet))}
	}
	return nil
}

// Webhook POSTs every event as JSON to a URL.
type Webhook struct {
	url    string
	client *http.Client
}

// NewWebhook creates a webhook notifier for rawURL, which should be an
// absolute http or https URL.
func NewWebhook(rawURL string) *Webhook {
	return &Webhook{url: rawURL, client: defaultClient}
}

// Name implements Notifier.
func (w *Webhook) Name() string { return "webhook" }

// Target implements Notifier.
func (w *Webhook) Target() string { return target(w.url) }

// Send implements Notifier.
func (w *Webhook) Send(ctx context.Context, e Event) error {
	// No HTML escaping, so the op reads ">" rather than "\u003e".
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return err
	}
	return post(ctx, w.client, w.url, http.Header{"Content-Type": {"application/json"}}, bytes.TrimSuffix(body.Bytes(), []byte("\n")))
}

// Ntfy publishes every event to an ntfy topic, with a title, a priority
// that follows the severity and emoji tags. See https://docs.ntfy.sh/publish/.
type Ntfy struct {
	url    string
	client *http.Client
}

// NewNtfy creates an ntfy notifier for a topic URL such as
// https://ntfy.sh/my-pi-alerts. Credentials can go in the URL as user info
// for basic auth.
func NewNtfy(topicURL string) *Ntfy {
	return &Ntfy{url: topicURL, client: defaultClient}
}

// Name implements Notifier.
func (n *Ntfy) Name() string { return "ntfy" }

// Target implements Notifier.
func (n *Ntfy) Target() string { return target(n.url) }

// ntfy priorities, see https://docs.ntfy.sh/publish/#message-priority.
const (
	ntfyDefault = "3"
	ntfyHigh    = "4"
	ntfyUrgent  = "5"
)

// Send implements Notifier.
func (n *Ntfy) Send(ctx context.Context, e Event) error {
	h := http.Header{}
	h.Set("Title", encodeHeader(ntfyTitle(e)))
	h.Set("Priority", ntfyPriority(e))
	h.Set("Tags", strings.Join(ntfyTags(e), ","))
	h.Set("Content-Type", "text/plain; charset=utf-8")
	return post(ctx, n.client, n.url, h, []byte(ntfyMessage(e)))
}

// ntfyTitle reads like "pi5: cpu-temp-high firing".
func ntfyTitle(e Event) string {
	title := e.Alert + " " + string(e.State)
	if e.Hostname != "" {
		title = e.Hostname + ": " + title
	}
	return title
}

// ntfyMessage reads like "temp.cpu_c is 78.2, above the threshold of 75."
func ntfyMessage(e Event) string {
	side := "above"
	if e.Op == alerts.OpBelow {
		side = "below"
	}
	if e.State == alerts.StateResolved {
		return fmt.Sprintf("%s is back to %g; the alert fires %s %g.", e.Series, e.Value, side, e.Threshold)
	}
	return fmt.Sprintf("%s is %g, %s the threshold of %g.", e.Series, e.Value, side, e.Threshold)
}

// ntfyPriority makes critical alerts urgent and warnings high. A resolve is
// good news and goes out at the default priority.
func ntfyPriority(e Event) string {
	switch {
	case e.State == alerts.StateResolved:
		return ntfyDefault
	case e.Severity == alerts.SeverityCritical:
		return ntfyUrgent
	default:
		return ntfyHigh
	}
}

// ntfyTags starts with an emoji short code, which ntfy shows in front of
// the title, followed by the severity as a plain tag.
func ntfyTags(e Event) []string {
	emoji := "warning"
	switch {
	case e.State == alerts.StateResolved:
		emoji = "white_check_mark"
	case e.Severity == alerts.SeverityCritical:
		emoji = "rotating_light"
	}
	return []string{emoji, string(e.Severity)}
}

// encodeHeader RFC 2047 encodes s if it is not plain ASCII, which ntfy
// decodes, since rule and host names may hold any character.
func encodeHeader(s string) string {
	return mime.QEncoding.Encode("utf-8", s)
}
