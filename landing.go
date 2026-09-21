package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// The custom landing page lives in the homepage layout: a JSON array of
// widgets served by RestAPIService at /api/v1/ui/layout/homepage. The
// landing-page text is the `greeter` widget's metadata.content (markdown); the
// server renders it to sanitized HTML in metadata.renderedContent on save.
//
// (JuliaHub#24048 removed the old GET /app/homepage and POST/DELETE
// /app/config/homepage endpoints; see JuliaComputing/jh#58.)
const homepageLayoutPath = "/api/v1/ui/layout/homepage"

// Widget type carrying the landing-page markdown.
const greeterWidgetType = "greeter"

// Geometry used when `update` has to add a greeter to a layout without one:
// the same slot the platform's default layout puts it in (full width, first row).
const (
	greeterDefaultTitle = "Welcome"
	greeterDefaultW     = 2
	greeterDefaultH     = 1
)

// layoutWidget is one homepage widget. Only the fields the CLI reads or
// rewrites are typed; everything else is kept verbatim in Extra so a layout
// round-trips through `update`/`remove` without the CLI having to know every
// widget type and field the frontend uses.
type layoutWidget struct {
	Order    int             `json:"order"`
	Title    string          `json:"title"`
	Type     string          `json:"type"`
	X        int             `json:"x"`
	Y        int             `json:"y"`
	W        int             `json:"w"`
	H        int             `json:"h"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	Extra    map[string]json.RawMessage
}

// widgetMetadata is the greeter/markdown widget metadata the CLI edits.
// renderedContent is intentionally not set by the CLI: the server recomputes
// it from content on save (and ignores client-supplied values).
type widgetMetadata struct {
	SubType         json.RawMessage `json:"subType"`
	Content         string          `json:"content"`
	RenderedContent string          `json:"renderedContent,omitempty"`
}

var layoutWidgetKnownKeys = map[string]bool{
	"order": true, "title": true, "type": true,
	"x": true, "y": true, "w": true, "h": true, "metadata": true,
}

func (w *layoutWidget) UnmarshalJSON(data []byte) error {
	type plain layoutWidget
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	for k := range all {
		if layoutWidgetKnownKeys[k] {
			delete(all, k)
		}
	}
	*w = layoutWidget(p)
	w.Extra = all
	return nil
}

func (w layoutWidget) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(w.Extra)+8)
	for k, v := range w.Extra {
		out[k] = v
	}
	put := func(k string, v interface{}) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		out[k] = b
		return nil
	}
	for k, v := range map[string]interface{}{
		"order": w.Order, "title": w.Title, "type": w.Type,
		"x": w.X, "y": w.Y, "w": w.W, "h": w.H,
	} {
		if err := put(k, v); err != nil {
			return nil, err
		}
	}
	if len(w.Metadata) > 0 {
		out["metadata"] = w.Metadata
	}
	return json.Marshal(out)
}

// greeterContent returns the markdown of the first greeter widget, and whether
// one exists.
func greeterContent(widgets []layoutWidget) (string, bool) {
	for _, w := range widgets {
		if w.Type != greeterWidgetType {
			continue
		}
		var md widgetMetadata
		if len(w.Metadata) > 0 {
			_ = json.Unmarshal(w.Metadata, &md)
		}
		return md.Content, true
	}
	return "", false
}

// setGreeterContent replaces the content of every greeter widget in the layout.
// If the layout has no greeter, one is prepended in the default slot (first
// row, full width), every other widget is pushed down a row, and `order` is
// renumbered contiguously after the greeter — the same way the platform
// migrated existing layouts when the greeter was introduced. It returns the
// new layout and whether a greeter was added.
func setGreeterContent(widgets []layoutWidget, content string) ([]layoutWidget, bool, error) {
	found := false
	for i := range widgets {
		if widgets[i].Type != greeterWidgetType {
			continue
		}
		var md widgetMetadata
		if len(widgets[i].Metadata) > 0 {
			if err := json.Unmarshal(widgets[i].Metadata, &md); err != nil {
				return nil, false, fmt.Errorf("widget %q has unreadable metadata: %w", widgets[i].Title, err)
			}
		}
		if len(md.SubType) == 0 {
			md.SubType = json.RawMessage("null")
		}
		md.Content = content
		md.RenderedContent = ""
		b, err := json.Marshal(md)
		if err != nil {
			return nil, false, err
		}
		widgets[i].Metadata = b
		found = true
	}
	if found {
		return widgets, false, nil
	}

	md, err := json.Marshal(widgetMetadata{SubType: json.RawMessage("null"), Content: content})
	if err != nil {
		return nil, false, err
	}
	greeter := layoutWidget{
		Order: 0, Title: greeterDefaultTitle, Type: greeterWidgetType,
		X: 0, Y: 0, W: greeterDefaultW, H: greeterDefaultH,
		Metadata: md,
	}
	rest := make([]layoutWidget, len(widgets))
	copy(rest, widgets)
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].Order < rest[j].Order })
	out := make([]layoutWidget, 0, len(widgets)+1)
	out = append(out, greeter)
	for i, w := range rest {
		w.Y += greeterDefaultH
		w.Order = i + 1
		out = append(out, w)
	}
	return out, true, nil
}

// removeGreeter drops every greeter widget from the layout, re-numbering
// `order` so it stays contiguous. It returns the new layout and how many
// widgets were removed. Positions are left alone: the frontend grid packs
// rows on render.
func removeGreeter(widgets []layoutWidget) ([]layoutWidget, int) {
	out := make([]layoutWidget, 0, len(widgets))
	removed := 0
	for _, w := range widgets {
		if w.Type == greeterWidgetType {
			removed++
			continue
		}
		out = append(out, w)
	}
	if removed == 0 {
		return widgets, 0
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	for i := range out {
		out[i].Order = i
	}
	return out, removed
}

// layoutRequest performs one authenticated call against the homepage layout
// endpoint and returns the response body on 200. Error messages name the URL
// so that a removed or moved endpoint is diagnosable from the CLI output.
func layoutRequest(server, method string, body io.Reader, permissionErr string) ([]byte, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required — run 'jh auth login' first")
	}

	url := fmt.Sprintf("https://%s%s", server, homepageLayoutPath)
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("could not prepare request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the server: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("could not read response from server: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("%s (status %d from %s %s)", permissionErr, resp.StatusCode, method, url)
	case resp.StatusCode != http.StatusOK:
		msg := strings.TrimSpace(apiErrorMessage(respBody))
		if msg != "" {
			return nil, fmt.Errorf("server returned status %d from %s %s: %s", resp.StatusCode, method, url, msg)
		}
		return nil, fmt.Errorf("server returned status %d from %s %s", resp.StatusCode, method, url)
	}
	return respBody, nil
}

// apiErrorMessage pulls a human-readable message out of a RestAPIService
// error body ({"message": "..."} or {"error": "..."}), or returns "".
func apiErrorMessage(body []byte) string {
	var m struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return ""
	}
	if m.Message != "" {
		return m.Message
	}
	return m.Error
}

// fetchHomepageLayout GETs the current layout. An empty array means no layout
// is persisted and the frontend renders its built-in defaults.
func fetchHomepageLayout(server string) ([]layoutWidget, error) {
	body, err := layoutRequest(server, "GET", nil,
		"you do not have permission to view the landing page configuration")
	if err != nil {
		return nil, err
	}
	var widgets []layoutWidget
	if err := json.Unmarshal(body, &widgets); err != nil {
		return nil, fmt.Errorf("unexpected response from server: %w", err)
	}
	return widgets, nil
}

// saveHomepageLayout POSTs the whole layout; the server replaces the active
// layout atomically (soft-deletes the previous one).
func saveHomepageLayout(server string, widgets []layoutWidget, permissionErr string) error {
	payload, err := json.Marshal(widgets)
	if err != nil {
		return fmt.Errorf("could not prepare request: %w", err)
	}
	body, err := layoutRequest(server, "POST", bytes.NewReader(payload), permissionErr)
	if err != nil {
		return err
	}
	var result struct {
		Success bool            `json:"success"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(body, &result); err == nil && !result.Success {
		var msg string
		_ = json.Unmarshal(result.Message, &msg)
		if msg == "" {
			msg = "server did not accept the layout"
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func showLandingPage(server string) error {
	widgets, err := fetchHomepageLayout(server)
	if err != nil {
		return err
	}
	if len(widgets) == 0 {
		fmt.Println("No custom content set: the home page uses the built-in default layout.")
		return nil
	}
	content, ok := greeterContent(widgets)
	if !ok {
		fmt.Println("No custom content set: the home page layout has no landing page (greeter) widget.")
		return nil
	}
	fmt.Println(content)
	return nil
}

func setLandingPage(server, content string) error {
	widgets, err := fetchHomepageLayout(server)
	if err != nil {
		return err
	}
	if len(widgets) == 0 {
		// The default layout is a frontend constant, so the CLI cannot
		// reconstruct it; saving just a greeter would drop every other card.
		return fmt.Errorf("the home page has no saved layout to add the landing page to — save a layout from the admin UI (Administrator → Settings → Custom Landing Page) first")
	}
	widgets, added, err := setGreeterContent(widgets, content)
	if err != nil {
		return err
	}
	if err := saveHomepageLayout(server, widgets,
		"you do not have permission to update the landing page"); err != nil {
		return err
	}
	if added {
		fmt.Println("Successfully updated the landing page (added a Welcome card to the home page layout).")
	} else {
		fmt.Println("Successfully updated the landing page.")
	}
	return nil
}

func removeLandingPage(server string) error {
	widgets, err := fetchHomepageLayout(server)
	if err != nil {
		return err
	}
	widgets, removed := removeGreeter(widgets)
	if removed == 0 {
		fmt.Println("No custom landing page to remove: the home page layout has no landing page (greeter) widget.")
		return nil
	}
	if err := saveHomepageLayout(server, widgets,
		"you do not have permission to remove the landing page"); err != nil {
		return err
	}
	fmt.Println("Successfully removed the custom landing page from the home page layout.")
	return nil
}

func readContentFromFileOrArgOrStdin(filePath, contentArg string) (string, error) {
	if filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("failed to read file %q: %w", filePath, err)
		}
		return string(data), nil
	}
	if contentArg != "" {
		return contentArg, nil
	}
	// Fall back to stdin
	fi, statErr := os.Stdin.Stat()
	if statErr != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return "", fmt.Errorf("no content provided — pass content as an argument, use --file, or pipe via stdin")
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("failed to read from stdin: %w", err)
	}
	return string(data), nil
}
