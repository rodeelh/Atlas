package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

// evalJSON marshals an Eval result value to JSON bytes.
// Returns nil if the result is JSON null.
func evalJSON(res interface{ MarshalJSON() ([]byte, error) }) ([]byte, bool) {
	raw, err := res.MarshalJSON()
	if err != nil || string(raw) == "null" {
		return nil, false
	}
	return raw, true
}

// ── Right / double click ──────────────────────────────────────────────────────

func (m *Manager) RightClick(ctx context.Context, selector string, waitAfterMs int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, err := m.activePage()
	if err != nil {
		return err
	}
	el, err := pg.Context(ctx).Element(selector)
	if err != nil {
		return fmt.Errorf("browser: right_click target %q not found: %w", selector, err)
	}
	if err := el.Click(proto.InputMouseButtonRight, 1); err != nil {
		return fmt.Errorf("browser: right_click %q: %w", selector, err)
	}
	if waitAfterMs > 0 {
		time.Sleep(time.Duration(waitAfterMs) * time.Millisecond)
	}
	return nil
}

func (m *Manager) DoubleClick(ctx context.Context, selector string, waitAfterMs int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, err := m.activePage()
	if err != nil {
		return err
	}
	el, err := pg.Context(ctx).Element(selector)
	if err != nil {
		return fmt.Errorf("browser: double_click target %q not found: %w", selector, err)
	}
	if err := el.Click(proto.InputMouseButtonLeft, 2); err != nil {
		return fmt.Errorf("browser: double_click %q: %w", selector, err)
	}
	if waitAfterMs > 0 {
		time.Sleep(time.Duration(waitAfterMs) * time.Millisecond)
	}
	return nil
}

// ── Keyboard ──────────────────────────────────────────────────────────────────

// KeyPress presses a key or modifier combo on the active page.
// Accepts names like "Enter", "Escape", "Tab", "ArrowDown", "F5", or combos
// like "Ctrl+A", "Shift+Tab", "Meta+R".
func (m *Manager) KeyPress(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	page, err := m.ensurePage()
	if err != nil {
		return err
	}

	parts := strings.Split(key, "+")
	modifiers := make([]input.Key, 0, len(parts)-1)
	for _, mod := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(mod)) {
		case "ctrl", "control":
			modifiers = append(modifiers, input.ControlLeft)
		case "shift":
			modifiers = append(modifiers, input.ShiftLeft)
		case "alt":
			modifiers = append(modifiers, input.AltLeft)
		case "meta", "cmd", "command":
			modifiers = append(modifiers, input.MetaLeft)
		}
	}

	mainKey := normalizeKeyName(strings.TrimSpace(parts[len(parts)-1]))

	for _, mod := range modifiers {
		if err := page.Keyboard.Press(mod); err != nil {
			// Release already-pressed modifiers before returning.
			for _, released := range modifiers {
				_ = page.Keyboard.Release(released)
				if released == mod {
					break
				}
			}
			return fmt.Errorf("browser: key_press modifier %q: %w", mod, err)
		}
	}

	typeErr := page.Keyboard.Type(mainKey)

	for i := len(modifiers) - 1; i >= 0; i-- {
		_ = page.Keyboard.Release(modifiers[i])
	}

	if typeErr != nil {
		return fmt.Errorf("browser: key_press %q: %w", key, typeErr)
	}
	return nil
}

func normalizeKeyName(key string) input.Key {
	switch strings.ToLower(key) {
	case "enter", "return":
		return input.Enter
	case "escape", "esc":
		return input.Escape
	case "tab":
		return input.Tab
	case "backspace":
		return input.Backspace
	case "delete", "del":
		return input.Delete
	case "space", " ":
		return input.Key(' ')
	case "arrowup", "up":
		return input.ArrowUp
	case "arrowdown", "down":
		return input.ArrowDown
	case "arrowleft", "left":
		return input.ArrowLeft
	case "arrowright", "right":
		return input.ArrowRight
	case "home":
		return input.Home
	case "end":
		return input.End
	case "pageup":
		return input.PageUp
	case "pagedown":
		return input.PageDown
	case "insert":
		return input.Insert
	case "f1":
		return input.F1
	case "f2":
		return input.F2
	case "f3":
		return input.F3
	case "f4":
		return input.F4
	case "f5":
		return input.F5
	case "f6":
		return input.F6
	case "f7":
		return input.F7
	case "f8":
		return input.F8
	case "f9":
		return input.F9
	case "f10":
		return input.F10
	case "f11":
		return input.F11
	case "f12":
		return input.F12
	default:
		if len(key) == 1 {
			return input.Key(rune(key[0]))
		}
		return input.Enter // unknown key — fallthrough to Enter as safe default
	}
}

// ── Navigation history ────────────────────────────────────────────────────────

func (m *Manager) GoBack(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, err := m.activePage()
	if err != nil {
		return err
	}
	if _, err := pg.Context(ctx).Eval(`() => history.back()`); err != nil {
		return fmt.Errorf("browser: go_back: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = pg.Context(waitCtx).WaitLoad()
	return nil
}

func (m *Manager) GoForward(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, err := m.activePage()
	if err != nil {
		return err
	}
	if _, err := pg.Context(ctx).Eval(`() => history.forward()`); err != nil {
		return fmt.Errorf("browser: go_forward: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = pg.Context(waitCtx).WaitLoad()
	return nil
}

func (m *Manager) Reload(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	page, err := m.ensurePage()
	if err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := page.Context(waitCtx).Eval(`() => location.reload()`); err != nil {
		return fmt.Errorf("browser: reload: %w", err)
	}
	_ = page.Context(waitCtx).WaitLoad()
	return nil
}

// ── HTML extraction ───────────────────────────────────────────────────────────

func (m *Manager) GetHTML(ctx context.Context, selector string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, err := m.activePage()
	if err != nil {
		return "", err
	}

	var jsExpr string
	if selector != "" {
		jsExpr = fmt.Sprintf(`() => { const el = document.querySelector(%q); return el ? el.outerHTML : null; }`, selector)
	} else {
		jsExpr = `() => document.documentElement.outerHTML`
	}

	res, err := pg.Context(ctx).Eval(jsExpr)
	if err != nil {
		return "", fmt.Errorf("browser: get_html eval: %w", err)
	}
	raw, ok := evalJSON(res.Value)
	if !ok {
		return "", fmt.Errorf("browser: no element found for selector %q", selector)
	}

	var html string
	if err := json.Unmarshal(raw, &html); err != nil {
		html = res.Value.String()
	}
	const maxBytes = 100 * 1024
	if len(html) > maxBytes {
		html = html[:maxBytes] + "\n<!-- truncated -->"
	}
	return html, nil
}

// ── Multi-element queries ─────────────────────────────────────────────────────

// FindAll returns text content (or attribute values) of all elements matching selector.
func (m *Manager) FindAll(ctx context.Context, selector, attribute string, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, err := m.activePage()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	var jsExpr string
	if attribute != "" {
		jsExpr = fmt.Sprintf(
			`() => Array.from(document.querySelectorAll(%q)).slice(0,%d).map(el => el.getAttribute(%q) ?? '')`,
			selector, limit, attribute)
	} else {
		jsExpr = fmt.Sprintf(
			`() => Array.from(document.querySelectorAll(%q)).slice(0,%d).map(el => (el.innerText||el.textContent||'').trim())`,
			selector, limit)
	}

	res, err := pg.Context(ctx).Eval(jsExpr)
	if err != nil {
		return nil, fmt.Errorf("browser: find_all eval: %w", err)
	}

	raw, _ := res.Value.MarshalJSON()
	var results []string
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, fmt.Errorf("browser: find_all parse: %w", err)
	}
	return results, nil
}

// GetInputValue returns the current value of an input, textarea, or select element.
func (m *Manager) GetInputValue(ctx context.Context, selector string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, err := m.activePage()
	if err != nil {
		return "", err
	}

	res, err := pg.Context(ctx).Eval(fmt.Sprintf(`() => {
		const el = document.querySelector(%q);
		if (!el) return null;
		return el.value !== undefined ? el.value : el.innerText;
	}`, selector))
	if err != nil {
		return "", fmt.Errorf("browser: get_input_value eval: %w", err)
	}
	raw, ok := evalJSON(res.Value)
	if !ok {
		return "", fmt.Errorf("browser: element %q not found", selector)
	}

	var val string
	if err := json.Unmarshal(raw, &val); err != nil {
		return res.Value.String(), nil
	}
	return val, nil
}

// ── Viewport ──────────────────────────────────────────────────────────────────

func (m *Manager) SetViewport(width, height int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	page, err := m.ensurePage()
	if err != nil {
		return err
	}
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 800
	}
	return page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width: width, Height: height, DeviceScaleFactor: 1,
	})
}

// ── Cookies ───────────────────────────────────────────────────────────────────

func (m *Manager) GetCookies(ctx context.Context) ([]StoredCookie, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	page, err := m.ensurePage()
	if err != nil {
		return nil, err
	}

	info, err := page.Info()
	if err != nil {
		return nil, fmt.Errorf("browser: get_cookies page info: %w", err)
	}

	cookies, err := page.Cookies([]string{info.URL})
	if err != nil {
		return nil, fmt.Errorf("browser: get_cookies: %w", err)
	}

	result := make([]StoredCookie, 0, len(cookies))
	for _, c := range cookies {
		result = append(result, StoredCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   bool(c.Secure),
			HTTPOnly: bool(c.HTTPOnly),
			Expires:  float64(c.Expires),
		})
	}
	return result, nil
}

func (m *Manager) SetCookie(ctx context.Context, name, value, domain, path string, secure bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	page, err := m.ensurePage()
	if err != nil {
		return err
	}
	if path == "" {
		path = "/"
	}
	return page.SetCookies([]*proto.NetworkCookieParam{
		{Name: name, Value: value, Domain: domain, Path: path, Secure: secure},
	})
}

func (m *Manager) DeleteCookies(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	page, err := m.ensurePage()
	if err != nil {
		return err
	}
	return proto.NetworkClearBrowserCookies{}.Call(page)
}

// ── localStorage ──────────────────────────────────────────────────────────────

func (m *Manager) GetLocalStorage(ctx context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, err := m.activePage()
	if err != nil {
		return "", err
	}

	res, err := pg.Context(ctx).Eval(fmt.Sprintf(`() => window.localStorage.getItem(%q)`, key))
	if err != nil {
		return "", fmt.Errorf("browser: get_local_storage eval: %w", err)
	}
	raw, ok := evalJSON(res.Value)
	if !ok {
		return "", nil // key not present
	}
	var val string
	if err := json.Unmarshal(raw, &val); err != nil {
		return res.Value.String(), nil
	}
	return val, nil
}

func (m *Manager) SetLocalStorage(ctx context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, err := m.activePage()
	if err != nil {
		return err
	}
	_, err = pg.Context(ctx).Eval(fmt.Sprintf(`() => window.localStorage.setItem(%q, %q)`, key, value))
	if err != nil {
		return fmt.Errorf("browser: set_local_storage eval: %w", err)
	}
	return nil
}

// ── PDF ───────────────────────────────────────────────────────────────────────

// SavePDF renders the current page to PDF and returns the raw bytes.
func (m *Manager) SavePDF(ctx context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	page, err := m.ensurePage()
	if err != nil {
		return nil, err
	}
	reader, err := page.PDF(&proto.PagePrintToPDF{PrintBackground: true})
	if err != nil {
		return nil, fmt.Errorf("browser: save_pdf: %w", err)
	}
	return io.ReadAll(reader)
}

// ── Dialog handling ───────────────────────────────────────────────────────────

// AcceptDialog accepts the currently open alert/confirm/prompt dialog.
// For prompt dialogs, promptText is typed into the input before accepting.
func (m *Manager) AcceptDialog(ctx context.Context, promptText string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	page, err := m.ensurePage()
	if err != nil {
		return err
	}
	return proto.PageHandleJavaScriptDialog{Accept: true, PromptText: promptText}.Call(page)
}

// DismissDialog dismisses (cancels) the currently open dialog.
func (m *Manager) DismissDialog(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	page, err := m.ensurePage()
	if err != nil {
		return err
	}
	return proto.PageHandleJavaScriptDialog{Accept: false}.Call(page)
}

// ── Checkbox / radio ──────────────────────────────────────────────────────────

// Check sets the checked state of a checkbox or radio input and fires change/input events.
func (m *Manager) Check(ctx context.Context, selector string, checked bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, err := m.activePage()
	if err != nil {
		return err
	}

	checkedStr := "false"
	if checked {
		checkedStr = "true"
	}
	_, err = pg.Context(ctx).Eval(fmt.Sprintf(`() => {
		const el = document.querySelector(%q);
		if (!el) return false;
		if (el.checked !== %s) {
			el.checked = %s;
			el.dispatchEvent(new Event('input', {bubbles:true}));
			el.dispatchEvent(new Event('change', {bubbles:true}));
		}
		return true;
	}`, selector, checkedStr, checkedStr))
	if err != nil {
		return fmt.Errorf("browser: check %q: %w", selector, err)
	}
	return nil
}

// ── Drag and drop ─────────────────────────────────────────────────────────────

// DragDrop drags fromSel to toSel using viewport-coordinate mouse events.
func (m *Manager) DragDrop(ctx context.Context, fromSel, toSel string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	page, err := m.ensurePage()
	if err != nil {
		return err
	}

	type point struct{ X, Y float64 }

	getCenter := func(sel string) (point, error) {
		res, err := page.Context(ctx).Eval(fmt.Sprintf(`() => {
			const el = document.querySelector(%q);
			if (!el) return null;
			const r = el.getBoundingClientRect();
			return {X: r.left + r.width/2, Y: r.top + r.height/2};
		}`, sel))
		if err != nil {
			return point{}, fmt.Errorf("element %q not found: %w", sel, err)
		}
		raw, ok := evalJSON(res.Value)
		if !ok {
			return point{}, fmt.Errorf("element %q not found", sel)
		}
		var pt point
		if err := json.Unmarshal(raw, &pt); err != nil {
			return point{}, fmt.Errorf("parse coords for %q: %w", sel, err)
		}
		return pt, nil
	}

	from, err := getCenter(fromSel)
	if err != nil {
		return fmt.Errorf("browser: drag_drop source: %w", err)
	}
	to, err := getCenter(toSel)
	if err != nil {
		return fmt.Errorf("browser: drag_drop target: %w", err)
	}

	if err := page.Mouse.MoveTo(proto.Point{X: from.X, Y: from.Y}); err != nil {
		return fmt.Errorf("browser: drag_drop move to source: %w", err)
	}
	if err := page.Mouse.Down(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("browser: drag_drop press: %w", err)
	}
	if err := page.Mouse.MoveLinear(proto.Point{X: to.X, Y: to.Y}, 10); err != nil {
		_ = page.Mouse.Up(proto.InputMouseButtonLeft, 1)
		return fmt.Errorf("browser: drag_drop move to target: %w", err)
	}
	if err := page.Mouse.Up(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("browser: drag_drop release: %w", err)
	}
	return nil
}

// ── Focus ─────────────────────────────────────────────────────────────────────

func (m *Manager) Focus(ctx context.Context, selector string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, err := m.activePage()
	if err != nil {
		return err
	}
	el, err := pg.Context(ctx).Element(selector)
	if err != nil {
		return fmt.Errorf("browser: focus target %q not found: %w", selector, err)
	}
	return el.Focus()
}

// ── Table extraction ──────────────────────────────────────────────────────────

// ExtractTable parses an HTML table into a 2-D slice of cell text.
// selector identifies the <table> element; defaults to the first table on the page.
func (m *Manager) ExtractTable(ctx context.Context, selector string) ([][]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pg, err := m.activePage()
	if err != nil {
		return nil, err
	}

	if selector == "" {
		selector = "table"
	}

	res, err := pg.Context(ctx).Eval(fmt.Sprintf(`() => {
		const table = document.querySelector(%q);
		if (!table) return null;
		return Array.from(table.querySelectorAll('tr')).map(tr =>
			Array.from(tr.querySelectorAll('th,td')).map(cell => (cell.innerText||'').trim())
		).filter(row => row.length > 0);
	}`, selector))
	if err != nil {
		return nil, fmt.Errorf("browser: extract_table eval: %w", err)
	}
	raw, ok := evalJSON(res.Value)
	if !ok {
		return nil, fmt.Errorf("browser: no table found for selector %q", selector)
	}

	var rows [][]string
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("browser: extract_table parse: %w", err)
	}
	return rows, nil
}

// ── PDF save helper (used by skill layer) ─────────────────────────────────────

// SavePDFToFile renders the page as PDF and writes it to path.
// If path is empty it defaults to ~/Downloads/atlas-page-<unix>.pdf.
func SavePDFToFile(ctx context.Context, m *Manager, path string) (string, error) {
	data, err := m.SavePDF(ctx)
	if err != nil {
		return "", err
	}
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, "Downloads", fmt.Sprintf("atlas-page-%d.pdf", time.Now().Unix()))
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("write PDF: %w", err)
	}
	return path, nil
}
