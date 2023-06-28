package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
	"unicode"

	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"
)

// ==========================================
// =============== Defines =================
// ==========================================

const KILO_VERSION = "0.0.1"
const KILO_TAB_STOP = 8
const KILO_MESSAGE_TIMEOUT = 5
const KILO_QUIT_TIMES = 3

// Define keys we care about and give them really high numbers
// to avoid conflict with existing keys.
const (
	BACKSPACE  = 127
	ARROW_LEFT = 1000 + iota
	ARROW_RIGHT
	ARROW_UP
	ARROW_DOWN
	DEL_KEY
	HOME_KEY
	END_KEY
	PAGE_UP
	PAGE_DOWN
)

const RED = 31
const BLUE = 34
const WHITE = 37
const DEFAULT = 39

const (
	HL_NORMAL uint8 = iota
	HL_NUMBER
	HL_MATCH
)

var syntaxColors = map[uint8]int{
	HL_NUMBER: RED,
	HL_MATCH:  BLUE,
}

const ESC = '\x1b' // 27

// CTRL_KEY is a mask for the control keys,
// stripping bits 5 and 6 from the character code, k.
func CTRL_KEY(k rune) int {
	return int(k) & 0x1f
}

func MIN(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func MAX(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ==========================================
// ================= Data ===================
// ==========================================

// Maintain state of the editor.
type editorConfig struct {
	originalTermios *unix.Termios
	// terminal size
	screenrows, screencols int
	// cursor position
	cx, cy int
	// render position. equals cx if there are no tabs
	rx int
	// The rows of text in the editor
	rows []editorRow
	// Whether or not the file has been modified since opening/saving
	dirty bool
	// The number of rows in the editor
	numrows int
	// Current row the user is scrolled to
	rowOffset int
	// Current column the user is scrolled to
	colOffset int
	// The filename to display in the status bar.
	filename string
	// Status message text
	statusMsg string
	// Timestamp for the status message, used to determine how long it's been shown.
	statusMsgTime time.Time
}

var config editorConfig

// Holds the main viewport of the editor.
var mainBuffer strings.Builder

// Represents a row of text in the editor.
type editorRow struct {
	// The literal text of the row.
	content string
	// Our render of the content, with tabs expanded.
	render []rune
	// The syntax-highlight properties for the row.
	// Each position corresponds to a character in the render string.
	highlights []uint8
}

// Track how many times Quit has been attempted
// This is done with a static variable in the original C code
// but Go doesn't have static variables.
var quitTimes = KILO_QUIT_TIMES

func (e editorRow) Len() int {
	return len(e.content)
}

func (e editorRow) RLen() int {
	return len(e.render)
}

// ==========================================
// =============== Terminal =================
// ==========================================

// Thin wrapper around panic to gracefully exit.
func exit() {
	if r := recover(); r != nil {
		cleanScreen(&mainBuffer)
		fmt.Print(mainBuffer.String())
		log.Fatalf("%+v. Quitting kilo...\r\n", r)
	}
}

// enableRawMode turns on raw mode for the terminal. It remembers the settings of the terminal
// before the change so it can restore it later.
//
// Raw mode (as opposed to canonical mode) sends each input directly to program
// instead of buffering it and sending it when Enter is pressed.
func enableRawMode() {
	var err error
	config.originalTermios, err = unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS)
	if err != nil {
		panic("Failed to obtain terminal settings: " + err.Error())
	}
	raw := *config.originalTermios
	// IXON: disable flow control
	// ICRNL: disable CR to NL conversion
	// BRKINT: disable break conditions from causing SIGINT
	// INPCK: disable parity check
	// ISTRIP: disable stripping of eighth bit fo input byte
	raw.Iflag &^= unix.IXON | unix.ICRNL | unix.BRKINT | unix.INPCK | unix.ISTRIP
	// OPOST: disable output processing
	raw.Oflag &^= unix.OPOST
	// CS8: Set character size to 8-bits
	raw.Cflag |= unix.CS8
	// ECHO: disable echo
	// ICANON: disable canonical mode
	// ISIG: disable signals like SIGINT and SIGTSTP
	// IEXTEN: disable extended input processing
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.ISIG | unix.IEXTEN
	// VMIN: Minimum number of characters for noncanonical read
	// VTIME: Timeout in deciseconds for noncanonical read
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 1

	unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, &raw)
}

// disableRawMode restores the terminal to its previous settings.
func disableRawMode() {
	if err := unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, config.originalTermios); err != nil {
		panic("Failed to restore terminal settings: " + err.Error())
	}
}

// editorReadKey waits for and returns a single keypress from the terminal.
func editorReadKey() (key int) {
	var err error
	var char rune
	// Point a Reader at STDIN
	reader := bufio.NewReader(os.Stdin)

	for {
		// Read a single character
		char, _, err = reader.ReadRune()
		if err != nil && err != io.EOF {
			panic("Failed to read character from terminal: " + err.Error())
		}
		if char != '\u0000' {
			break
		}
	}

	// Handle <esc>-ed sequence "keys"
	if char == ESC {
		var seq [3]rune

		// Read the next 2 bytes. If these fail, they probably typed <esc>
		seq[0], _, err = reader.ReadRune()
		if err != nil {
			return ESC
		}
		seq[1], _, err = reader.ReadRune()
		if err != nil {
			return ESC
		}

		if seq[0] == '[' {
			// Handle escape sequences like <esc>[4
			if seq[1] >= '0' && seq[1] <= '9' {
				seq[2], _, err = reader.ReadRune()
				if err != nil {
					// We don't recognize this sequence
					return ESC
				}
				// Handle escape sequences like <esc>[5~
				if seq[2] == '~' {
					switch seq[1] {
					case '1':
						return HOME_KEY
					case '3':
						return DEL_KEY
					case '4':
						return END_KEY
					case '5':
						return PAGE_UP
					case '6':
						return PAGE_DOWN
					case '7':
						return HOME_KEY
					case '8':
						return END_KEY
					}
				}
			} else {
				// Handle escape sequences like <esc>[A
				switch seq[1] {
				case 'A':
					return ARROW_UP
				case 'B':
					return ARROW_DOWN
				case 'C':
					return ARROW_RIGHT
				case 'D':
					return ARROW_LEFT
				case 'H':
					return HOME_KEY
				case 'F':
					return END_KEY
				}
			}
		} else if seq[0] == 'O' {
			// Handle escape sequences like <esc>OH
			switch seq[1] {
			case 'H':
				return HOME_KEY
			case 'F':
				return END_KEY
			}
		}
		// We don't recognize this sequence
		return ESC
	} else {
		return int(char)
	}
}

// getCursorPosition leverages low-level terminal requests to obtain the cursor position.
func getCursorPosition() (row int, col int, err error) {
	var buf [32]rune

	// Request cursor position
	fmt.Print("\x1b[6n\r\n")

	// Then read the response back from STDIN
	reader := bufio.NewReader(os.Stdin)
	for i := 0; i < len(buf); i++ {
		char, _, err := reader.ReadRune()
		if err != nil {
			if err == io.EOF {
				break
			}
			panic("Failed to read character from terminal: " + err.Error())
		}

		buf[i] = char

		if buf[i] == 'R' {
			break
		}
	}

	// We should have a response like:
	//     <esc>[24;80
	// where <esc> is \x1b
	// 24 is the row and 80 is the column
	if buf[0] != ESC || buf[1] != '[' {
		return 0, 0, errors.New("improper cursor position response")
	}

	// Parse the size
	_, err = fmt.Sscanf(string(buf[2:len(buf)-2]), "%d;%d", &row, &col)
	if err != nil {
		return 0, 0, err
	}

	return row, col, nil
}

// getWindowSize uses low-level terminal requests to obtain the window size.
func getWindowSize() (row int, col int) {
	winSize, err := unix.IoctlGetWinsize(int(os.Stdin.Fd()), unix.TIOCGWINSZ)
	if err != nil || winSize.Col == 0 {
		// As a fallback, shove the cursor in the bottom-right corner and record the cursor position.
		fmt.Print("\x1b[999C\x1b[999B")
		row, col, err = getCursorPosition()
		if err != nil {
			panic("Failed to obtain cursor position: " + err.Error())
		}
	} else {
		row = int(winSize.Row)
		col = int(winSize.Col)
	}

	return row, col
}

// ==========================================
// ========= Syntax Highlighting ============
// ==========================================

func editorSyntaxToColor(syntax uint8) int {
	if color, ok := syntaxColors[syntax]; ok {
		return color
	} else {
		return WHITE
	}
}

func editorUpdateSyntax(row *editorRow) {
	row.highlights = make([]uint8, len(row.render))

	for i, char := range row.render {
		if unicode.IsDigit(char) {
			row.highlights[i] = HL_NUMBER
		} else {
			row.highlights[i] = HL_NORMAL
		}
	}
}

// ==========================================
// ============ Row Operations ==============
// ==========================================

// Convert content x-coord to render x-coord.
// Basically, deal with tabs.
func editorRowCxToRx(row *editorRow, cx int) int {
	// Copy cx coordinates to rx, unless a tab is encountered.
	// Then, increment rx by the tab's width.
	rx := 0
	for _, char := range row.content[:cx] {
		if char == '\t' {
			// '\t' already consumes 1 space, so TAB_STOP - 1 is the total amount of tabs
			// Then, subtract off the amount of space already consumed in the TAB_STOP.
			rx += (KILO_TAB_STOP - 1) - (rx % KILO_TAB_STOP)
		}
		rx++
	}
	return rx
}

func editorRowRxToCx(row *editorRow, rx int) int {
	cx := 0
	currentRx := 0
	for _, char := range row.content {
		if char == '\t' {
			// '\t' already consumes 1 space, so TAB_STOP - 1 is the total amount of tabs
			// Then, subtract off the amount of space already consumed in the TAB_STOP.
			currentRx += (KILO_TAB_STOP - 1) - (currentRx % KILO_TAB_STOP)
		}
		currentRx++

		if currentRx > rx {
			return cx
		}
		cx++
	}
	// only hit if rx is larger than the row's length(?)
	return cx
}

// Fully render a row's content.
func editorUpdateRow(row *editorRow) {
	tabs := 0
	// Count how many tabs are in the row.
	for _, char := range row.content {
		if char == '\t' {
			tabs++
		}
	}

	// Allocate max space for the render, which is the content + expanded tabs.
	row.render = make([]rune, len(row.content)+(tabs*(KILO_TAB_STOP-1))+1)
	idx := 0
	// Copy content to render, replacing tabs with spaces.
	for _, char := range row.content {
		if char == '\t' {
			row.render[idx] = ' '
			idx++
			for ; idx%KILO_TAB_STOP != 0; idx++ {
				row.render[idx] = ' '
			}
		} else {
			row.render[idx] = char
			idx++
		}
	}
	row.render[idx] = '\x00'

	editorUpdateSyntax(row)
}

// Add a new row to global editor rows, ensuring to render it too.
func editorInsertRow(at int, rowContent string) {
	if at < 0 || at > config.numrows {
		return
	}

	config.rows = slices.Insert(config.rows, at, editorRow{content: rowContent})

	editorUpdateRow(&config.rows[config.numrows])
	config.numrows++
	config.dirty = true
}

// Insert a single character into row at the given index.
func editorRowInsertChar(row *editorRow, at int, char rune) {
	// Only allow inserts in a valid location.
	if at < 0 || at > row.Len() {
		at = row.Len()
	}
	// Insert the character and re-render the row.
	row.content = string(slices.Insert([]rune(row.content), at, char))
	editorUpdateRow(row)
	config.dirty = true
}

// Append a string to the end of a row
func editorRowAppendString(row *editorRow, s string) {
	row.content += s
	editorUpdateRow(row)
	config.dirty = true
}

// Remove a single character from row at the given index.
func editorRowDelChar(row *editorRow, at int) {
	// Don't delete from invalid locations.
	if at < 0 || at > row.Len() {
		return
	}

	// Delete character and re-render the row.
	row.content = string(slices.Delete([]rune(row.content), at, at+1))
	editorUpdateRow(row)
	config.dirty = true
}

// Remove an entire row
func editorDelRow(at int) {
	if at < 0 || at >= config.numrows {
		// nothing to delete
		return
	}
	slices.Delete(config.rows, at, at+1)
	config.numrows--
	config.dirty = true
}

// ==========================================
// ========== Editor Operations =============
// ==========================================

func editorInsertChar(char rune) {
	if config.cy == config.numrows {
		// Cursor on tilde lin after end of file, so we need a new row.
		editorInsertRow(config.numrows, "")
	}
	editorRowInsertChar(&config.rows[config.cy], config.cx, char)
	config.cx++
}

// Insert a newline when Enter is pressed
func editorInsertNewline() {
	if config.cx == 0 {
		// We're at the beginning of a line, so insert a new blank row
		editorInsertRow(config.cy, "")
	} else {
		// In the middle of a line, we need to split it
		rowContent := config.rows[config.cy].content[config.cx:]
		// Put content after the cursor on the next line
		editorInsertRow(config.cy+1, rowContent)
		// Get new reference to current row, it just changed
		row := &config.rows[config.cy]
		// Update current row to only include content before cursor
		row.content = row.content[0:config.cx]
		editorUpdateRow(row)
	}
	// Update cursor to new line.
	config.cy++
	config.cx = 0
}

func editorDelChar() {
	if config.cy == config.numrows {
		// Past end of file, nothing to delete
		return
	}
	if config.cx == 0 && config.cy == 0 {
		// At top left of file, nothing to delete
		return
	}

	row := &config.rows[config.cy]
	if config.cx > 0 {
		// We're not in the first column, delete the previous character.
		editorRowDelChar(row, config.cx-1)
		// Move the cursor back.
		config.cx--
	} else {
		// We're in the first column, delete the current row and append
		// its contents to previous row
		config.cx = config.rows[config.cy-1].Len()
		editorRowAppendString(&config.rows[config.cy-1], row.content)
		editorDelRow(config.cy)
		config.cy--
	}
}

// ==========================================
// =============== File I/O =================
// ==========================================

// Convert editor rows to one string.
func editorRowsToString(rows *[]editorRow) string {
	var result strings.Builder
	for _, row := range *rows {
		result.WriteString(row.content + "\n")
	}

	return result.String()
}

func editorOpen(filename string) {
	config.filename = filename
	// Open file for reading
	file, err := os.Open(filename)
	if err != nil {
		panic("Failed to open " + filename + " file: " + err.Error())
	}
	defer file.Close()

	// Read line
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		editorInsertRow(config.numrows, scanner.Text())
	}
	config.dirty = false
}

func editorSave() {
	if len(config.filename) == 0 {
		var err error
		config.filename, err = editorPrompt("Save as: %s", nil)
		if err != nil {
			editorSetStatusMessage("Save aborted: %s", err.Error())
		}
	}

	editorString := editorRowsToString(&config.rows)
	file, err := os.Create(config.filename)
	if err != nil {
		panic("Failed to create " + config.filename + " file: " + err.Error())
	}
	defer file.Close()

	_, err = file.WriteString(editorString)

	if err != nil {
		editorSetStatusMessage("Can't save! I/O error: %s", err.Error())
	} else {
		config.dirty = false
		editorSetStatusMessage("%d bytes written to disk", len(editorString))
	}
}

// ==========================================
// ================= Find ===================
// ==========================================
// TODO: No static variables in Go so these are global for now.
// -1 means no match
var lastMatch int

// 1 means forward, -1 means backward
var direction int

// Restore highlight after search
var savedHighlightIndex int = 0
var savedHighlights []uint8 = nil

func editorOnInputFind(query string, key int) {

	if savedHighlights != nil {
		// Restore highlights
		for i := range config.rows[savedHighlightIndex].render {
			config.rows[savedHighlightIndex].highlights[i] = savedHighlights[i]
		}
		savedHighlightIndex = 0
		savedHighlights = nil
	}

	if key == '\r' || key == ESC {
		// reset values
		lastMatch = -1
		direction = 1
		return
	} else if key == ARROW_RIGHT || key == ARROW_DOWN {
		direction = 1
	} else if key == ARROW_LEFT || key == ARROW_UP {
		direction = -1
	} else {
		// reset values
		lastMatch = -1
		direction = 1
	}

	if lastMatch == -1 {
		// no match, search forward
		direction = 1
	}

	// If there was a last match, currentRow is the line after (or before, if searching backwards).
	// If there wasn’t, it starts at the top of the file and searches in the forward direction to find the first match.
	currentRow := lastMatch
	for range config.rows {
		currentRow += direction

		// Wrap around search
		if currentRow == -1 {
			currentRow = config.numrows - 1
		} else if currentRow == config.numrows {
			currentRow = 0
		}

		row := &config.rows[currentRow]
		if matchIndex := strings.Index(string(row.render), query); matchIndex != -1 {
			// Set lastMatch so if user presses arrow keys, we search from this point
			lastMatch = currentRow
			config.cy = currentRow
			config.cx = editorRowRxToCx(row, matchIndex)
			// Put the finding at the top of the screen
			config.rowOffset = config.numrows

			// Record highlight
			savedHighlightIndex = currentRow
			savedHighlights = make([]uint8, row.RLen())
			for i := range row.render {
				savedHighlights[i] = row.highlights[i]
			}

			for i := range query {
				row.highlights[matchIndex+i] = HL_MATCH
			}
			break
		}
	}
}

// Find a string in the editor, with incremental search
func editorFind() {
	// Save current cursor and scrollback position
	currCx := config.cx
	currCy := config.cy
	curColOff := config.colOffset
	curRowOff := config.rowOffset

	query, _ := editorPrompt("Search: %s (Use ESC/Arrows/Enter)", editorOnInputFind)
	if len(query) == 0 {
		// User cancelled
		config.cx = currCx
		config.cy = currCy
		config.colOffset = curColOff
		config.rowOffset = curRowOff
	}
}

// ==========================================
// ================ Output ==================
// ==========================================

// Set the status message.
func editorSetStatusMessage(format string, args ...interface{}) {
	config.statusMsg = fmt.Sprintf(format, args...)
	config.statusMsgTime = time.Now()
}

// Draw the status bar at the bottom of the screen.
func editorDrawStatusBar(buf *strings.Builder) {
	// Invert colors
	buf.WriteString("\x1b[7m")

	// Add filename and line count.
	displayFilename := config.filename
	if len(config.filename) == 0 {
		displayFilename = "[No Name]"
	}
	dirtyStatus := ""
	if config.dirty {
		dirtyStatus = "(modified)"
	}
	status := fmt.Sprintf("%.20s - %d lines %s", displayFilename, config.numrows, dirtyStatus)
	// Truncate if longer than screen width.
	statusLen := MIN(len(status), config.screencols)
	buf.WriteString(status[0:statusLen])

	// Define right status view, showing current line number.
	rightStatus := fmt.Sprintf("%d/%d", config.cy+1, config.numrows)
	rightStatusLen := len(rightStatus)

	// Print the rest of the status.
	for statusLen < config.screencols {
		// Show the right status view, if it fits.
		if config.screencols-statusLen == rightStatusLen {
			buf.WriteString(rightStatus[0:rightStatusLen])
			// The entire status has been printed. Bail out.
			break
		} else {
			// Add a bunch of spaces, effectively making a white bar.
			buf.WriteRune(' ')
			statusLen++
		}
	}

	// Put the colors back to normal
	buf.WriteString("\x1b[m")
	// Put another newline, giving room for the status message.
	buf.WriteString("\r\n")
}

func editorDrawMessageBar(buf *strings.Builder) {
	// Clear any existing content
	buf.WriteString("\x1b[K")
	// Truncate message if it doesn't fit
	messageLen := MIN(len(config.statusMsg), config.screencols)
	// Show message, if it fits and is within timer bounds.
	if messageLen > 0 && time.Since(config.statusMsgTime).Seconds() < KILO_MESSAGE_TIMEOUT {
		buf.WriteString(config.statusMsg[0:messageLen])
	}
}

// editorScroll detects scroll based on cursor position.
func editorScroll() {
	config.rx = 0
	// If we have an active editor row, compute the render x-coord.
	if config.cy < config.numrows {
		config.rx = editorRowCxToRx(&config.rows[config.cy], config.cx)
	}

	// Check if cursor is above visible window
	if config.cy < config.rowOffset {
		config.rowOffset = config.cy
	}

	// Check if cursor is below visible window
	if config.cy >= config.rowOffset+config.screenrows {
		config.rowOffset = config.cy - config.screenrows + 1
	}

	// Check if cursor is to the left of visible window
	if config.rx < config.colOffset {
		config.colOffset = config.rx
	}

	// Check if cursor is to the right of visible window
	if config.rx >= config.colOffset+config.screencols {
		config.colOffset = config.rx - config.screencols + 1
	}
}

// editorRefreshScreen is called every cycle to repaint the screen.
func editorRefreshScreen() {
	// Compute screen position based on cursor position.
	editorScroll()

	// Hide the cursor before painting screen
	mainBuffer.WriteString("\x1b[?25l")
	// Reposition cursor to top left
	mainBuffer.WriteString("\x1b[H")

	// Draw all of the content, broken into rows.
	editorDrawRows(&mainBuffer)

	// Draw the status bar.
	editorDrawStatusBar(&mainBuffer)
	editorDrawMessageBar(&mainBuffer)

	// Draw cursor
	// +1 to put the cursor into terminal coordinates.
	// Account for scroll changing the screen position.
	fmt.Fprintf(&mainBuffer, "\x1b[%d;%dH", (config.cy-config.rowOffset)+1, (config.rx-config.colOffset)+1)
	// Bring the cursor back
	mainBuffer.WriteString("\x1b[?25h")

	// Flush the buffer to the screen.
	fmt.Print(mainBuffer.String())

	mainBuffer.Reset()
}

// Clear the entire screen
// https://vt100.net/docs/vt100-ug/chapter3.html#ED
func cleanScreen(buf *strings.Builder) {
	// Wipe screen
	buf.WriteString("\x1b[2J")
	// Reposition cursor to top left
	buf.WriteString("\x1b[H")
}

// editorDrawRows draws each visible line of the editor.
func editorDrawRows(buf *strings.Builder) {
	// Iterate over every row on the screen and determine the content that should be there.
	for y := 0; y < config.screenrows; y++ {
		// Figure out the line of the file we are viewing.
		fileRow := y + config.rowOffset
		if fileRow >= config.numrows {
			// The current line is outside of the file, what to draw?
			if config.numrows == 0 && y == config.screenrows/3 {
				// If the file is empty, show a welcome message.
				welcomeMsg := fmt.Sprintf("Kilo editor -- version %s", KILO_VERSION)
				welcomeLen := MIN(len(welcomeMsg), config.screencols)
				// Center the welcome message
				padding := (config.screencols - welcomeLen) / 2
				if padding > 0 {
					buf.WriteString("~")
					padding--
				}
				for ; padding > 0; padding-- {
					buf.WriteRune(' ')
				}
				// Truncate the welcome message to the screen width.
				buf.WriteString(welcomeMsg[0:welcomeLen])
			} else {
				// Fill the right column with tildes for the rest of the file.
				buf.WriteString("~")
			}
		} else {
			// Show the row contents
			// The size of the row is determined by the number of columns that have been scrolled
			// plus the render length
			rowSize := config.rows[fileRow].RLen() - config.colOffset
			// Don't allow negative row sizes.
			rowSize = MAX(rowSize, 0)
			// Don't allow row sizes greater than the screen width.
			rowSize = MIN(rowSize, config.screencols)
			// Track syntax color so we're not spamming escape sequences if the color doesn't change
			currentColor := DEFAULT
			// Draw the row if it should be shown, based on horizontal scroll
			if rowSize > config.colOffset {
				rowRender := string(config.rows[fileRow].render)
				truncatedRow := rowRender[config.colOffset:rowSize]
				highlights := config.rows[fileRow].highlights
				for i, char := range truncatedRow {
					if highlights[i] == HL_NORMAL {
						if currentColor != DEFAULT {
							buf.WriteString(fmt.Sprintf("\x1b[%dm", DEFAULT))
							currentColor = DEFAULT
						}
						buf.WriteRune(char)
					} else {
						color := editorSyntaxToColor(highlights[i])
						if color != currentColor {
							buf.WriteString(fmt.Sprintf("\x1b[%dm", color))
							currentColor = color
						}
						buf.WriteRune(char)
					}
				}
			}
			buf.WriteString(fmt.Sprintf("\x1b[%dm", DEFAULT))
		}

		// Delete the rest of the line. This effectively clears
		// the screen when this function runs the first time.
		buf.WriteString("\x1b[K")

		// Add new line to each row.
		buf.WriteString("\r\n")
	}
}

// ==========================================
// ================ Input ===================
// ==========================================

func editorPrompt(prompt string, onInput func(string, int)) (string, error) {
	var userInput string

	for {
		editorSetStatusMessage(prompt, userInput)
		editorRefreshScreen()

		char := editorReadKey()
		if char == DEL_KEY || char == CTRL_KEY('h') || char == BACKSPACE {
			if len(userInput) > 0 {
				userInput = userInput[0 : len(userInput)-1]
			}
		} else if char == ESC {
			editorSetStatusMessage("")
			if onInput != nil {
				onInput(userInput, char)
			}
			return "", errors.New("user cancelled")
		} else if char == '\r' {
			if len(userInput) > 0 {
				editorSetStatusMessage("")
				if onInput != nil {
					onInput(userInput, char)
				}
				return userInput, nil
			}
		} else if !unicode.IsControl(rune(char)) && char < 128 {
			userInput += string(rune(char))
		}

		if onInput != nil {
			onInput(userInput, char)
		}
	}
}

// Perform arithmetic to figure out new cursor position
func editorMoveCursor(key int) {
	// Fetch the current row so we can get it's dimensions and figure out how to move.
	var row string
	if config.cy < config.numrows {
		row = config.rows[config.cy].content
	}

	switch key {
	case ARROW_UP:
		// Move the cursor up one row if it's not already at the first row.
		if config.cy != 0 {
			config.cy--
		}
	case ARROW_LEFT:
		// Move the cursor left one column if it's not already at the first column.
		if config.cx != 0 {
			config.cx--
		} else if config.cy > 0 {
			// Cursor is already at the first column, move it to the end of the previous row.
			config.cy--
			config.cx = config.rows[config.cy].Len()
		}
	case ARROW_DOWN:
		// Move the cursor down one row if it's not already at the last row.
		if config.cy < config.numrows {
			config.cy++
		}
	case ARROW_RIGHT:
		// Move the cursor right one column if it's not already at the last column.
		if len(row) >= 0 && config.cx < len(row) {
			config.cx++
		} else if len(row) > 0 && config.cx == len(row) {
			// Cursor is already at the last column, move it to the beginning of the next row.
			config.cy++
			config.cx = 0
		}
	}

	// Re-calculate current row with new cursor position.
	if config.cy >= config.numrows {
		row = ""
	} else {
		row = config.rows[config.cy].content
	}

	rowLength := MAX(len(row), 0)

	// Snap cursor to the end of the row.
	if config.cx > rowLength {
		config.cx = rowLength
	}
}

// Handle user input
func editorProcessKeypress() bool {
	char := editorReadKey()

	switch char {
	case '\r':
		editorInsertNewline()
	case CTRL_KEY('q'):
		// Quit
		if config.dirty && quitTimes > 0 {
			editorSetStatusMessage("HEY!! The file has unsaved changes. Press Ctrl+Q %d more times to quit.", quitTimes)
			quitTimes--
			return true
		}
		cleanScreen(&mainBuffer)
		fmt.Print(mainBuffer.String())
		return false

	case CTRL_KEY('s'):
		editorSave()

	case CTRL_KEY('f'):
		editorFind()

	case HOME_KEY:
		// Move the cursor to the beginning of the current row
		config.cx = 0
	case END_KEY:
		// Move the cursor to the end of the current row if it's not already at the last row.
		if config.cy < config.numrows {
			config.cx = config.rows[config.cy].Len()
		}

	case BACKSPACE:
		fallthrough
	case CTRL_KEY('h'):
		fallthrough
	case DEL_KEY:
		if char == DEL_KEY {
			editorMoveCursor(ARROW_RIGHT)
		}
		editorDelChar()
	case PAGE_UP:
		fallthrough
	case PAGE_DOWN:
		// Move the cursor to the first/last visible row on the screen and scroll the view accordingly.
		if char == PAGE_UP {
			config.cy = config.rowOffset
		} else if char == PAGE_DOWN {
			config.cy = config.rowOffset + config.screenrows - 1
			config.cy = MIN(config.cy, config.numrows)
		}

		// PAGE_UP/PAGE_DOWN is implemented as repeated ARROW_UP/ARROW_DOWN movements.
		times := config.screenrows
		for ; 0 < times; times-- {
			if char == PAGE_UP {
				editorMoveCursor(ARROW_UP)
			} else {
				editorMoveCursor(ARROW_DOWN)
			}
		}

	case ARROW_UP:
		fallthrough
	case ARROW_LEFT:
		fallthrough
	case ARROW_DOWN:
		fallthrough
	case ARROW_RIGHT:
		editorMoveCursor(char)

	// Ignore these
	// Ctrl+l refreshes terminal screen but we're doing that all the time.
	case CTRL_KEY('l'):
		fallthrough
	case ESC:
		break

	default:
		editorInsertChar(rune(char))
	}

	// Reset counter
	quitTimes = KILO_QUIT_TIMES

	return true
}

// ==========================================
// ================= Main ===================
// ==========================================
// Set initial editor state.
func initializeEditor() {
	config.screenrows, config.screencols = getWindowSize()

	// Fool editorDrawRows into not drawing the last rows, which
	// we'll use for status
	config.screenrows -= 2
}

func main() {
	enableRawMode()
	defer exit()
	defer disableRawMode()
	initializeEditor()

	args := os.Args[1:]

	if len(args) >= 1 {
		editorOpen(args[0])
	}

	editorSetStatusMessage("HELP: Ctrl-Q - quit | Ctrl-S - save | Ctrl-F - find")

	for {
		editorRefreshScreen()
		if !editorProcessKeypress() {
			break
		}
	}
}
