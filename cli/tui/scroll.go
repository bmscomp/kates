package tui

// scrollWindow is the slice [start, end) of a list of count rows to draw in
// height rows so that the cursor stays on screen: the top of the list until
// the cursor passes the last row, then a window that ends on the cursor.
// Topics, groups and produce share it; each used to keep its own copy.
//
// The cursor is clamped into the list, and a height under one row still
// shows the cursor's row. An empty list is the empty window [0, 0).
func scrollWindow(cursor, count, height int) (start, end int) {
	if count <= 0 {
		return 0, 0
	}
	if height < 1 {
		height = 1
	}
	cursor = max(0, min(cursor, count-1))
	if cursor >= height {
		start = cursor - height + 1
	}
	return start, min(start+height, count)
}

// listRows is how many list rows fit a terminal height rows tall, below the
// tab bar, the list header and the help. A height too small to hold five
// (or unknown, before the first WindowSizeMsg) gives the old fixed 20.
func listRows(height int) int {
	if rows := height - 8; rows >= 5 {
		return rows
	}
	return 20
}
