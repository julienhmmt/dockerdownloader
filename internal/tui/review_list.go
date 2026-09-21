package tui

import "charm.land/lipgloss/v2"

// reviewChrome is the approximate number of terminal rows consumed by the
// review frame (title, subtitle, meta, help, border padding) that are not
// available for image rows.
const reviewChrome = 12

// listViewport returns the first visible index and number of rows that fit in
// a framed list body for the current terminal height, clamped to n items.
func (m model) listViewport(n, offset int) (start, visible int) {
	visible = m.height - reviewChrome
	if visible < 5 {
		visible = 5
	}
	if m.width == 0 || m.height == 0 {
		visible = 20 // tests / pre-WindowSizeMsg
	}
	if n == 0 {
		return 0, 0
	}
	if visible > n {
		visible = n
	}
	start = offset
	if start < 0 {
		start = 0
	}
	if start > n-visible {
		start = n - visible
	}
	return start, visible
}

// clampListCursor keeps cursor inside [0,n) and scrolls offset so the cursor
// stays inside the window of visible rows. n == 0 resets both.
func clampListCursor(n, cursor, offset, visible int) (int, int) {
	if n == 0 {
		return 0, 0
	}
	cursor = min(max(cursor, 0), n-1)
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+visible {
		offset = cursor - visible + 1
	}
	return cursor, max(offset, 0)
}

// reviewViewport returns the first visible index and number of image rows that
// fit in the review body for the current terminal height.
func (m model) reviewViewport() (start, visible int) {
	return m.listViewport(len(m.reviewImages), m.reviewOffset)
}

// ensureReviewCursorVisible scrolls reviewOffset so reviewCursor stays inside
// the visible window after navigation or list mutations.
func (m *model) ensureReviewCursorVisible() {
	_, visible := m.reviewViewport()
	m.reviewCursor, m.reviewOffset = clampListCursor(len(m.reviewImages), m.reviewCursor, m.reviewOffset, visible)
}

// cacheViewport returns the first visible index and number of cache rows that
// fit in the purge body for the current terminal height.
func (m model) cacheViewport() (start, visible int) {
	return m.listViewport(len(m.cacheEntries), m.cacheOffset)
}

// ensureCacheCursorVisible scrolls cacheOffset so cacheCursor stays inside the
// visible window after navigation or list mutations.
func (m *model) ensureCacheCursorVisible() {
	_, visible := m.cacheViewport()
	m.cacheCursor, m.cacheOffset = clampListCursor(len(m.cacheEntries), m.cacheCursor, m.cacheOffset, visible)
}

// reviewFrameInnerWidth returns the content width inside the review frame
// (panel width minus horizontal padding).
func (m model) reviewFrameInnerWidth() int {
	inner := maxFrameWidth
	if m.width > 0 {
		inner = max(0, min(maxFrameWidth, m.width-4))
	}
	// frame style pads 1,2 → horizontal padding of 4.
	return max(10, inner-4)
}

// reviewRowWidth is the full width of a review checklist row (for full-line hover).
func (m model) reviewRowWidth() int {
	return m.reviewFrameInnerWidth()
}

// reviewInnerWidth returns the max display width available for an image ref.
func (m model) reviewInnerWidth() int {
	// "▸ " (2) + "[x]" (3) + space (1) = 6 prefix cells before the ref.
	return max(10, m.reviewFrameInnerWidth()-6)
}

// truncateMiddle shortens s so its display width is at most maxW, replacing
// the middle with an ellipsis. Returns s unchanged when it already fits.
func truncateMiddle(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	if maxW <= 1 {
		return "…"
	}
	keep := maxW - 1
	left := keep / 2
	right := keep - left
	runes := []rune(s)
	if left+right >= len(runes) {
		return s
	}
	return string(runes[:left]) + "…" + string(runes[len(runes)-right:])
}
