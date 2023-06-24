package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// ==========================================
// =============== Defines* =================
// ==========================================

const KILO_VERSION = "0.0.1"

const (
	ARROW_LEFT = 1000 + iota
	ARROW_RIGHT
	ARROW_UP
	ARROW_DOWN
)

// CTRL_KEY is a mask for the control keys,
// stripping bits 5 and 6 from the character code, k.
func CTRL_KEY(k rune) int {
	return int(k) & 0x1f
}

// Find the minimum of two values.
func MIN(a, b int) int {
	if a < b {
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
}

var config editorConfig

var mainBuffer strings.Builder

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
	// Handle <esc>-ed sequences "keys"
	if char == '\x1b' {
		var seq [3]rune

		// Read the next 2 bytes. If these fail, they probably typed <esc>
		seq[0], _, err = reader.ReadRune()
		if err != nil {
			return '\x1b'
		}
		seq[1], _, err = reader.ReadRune()
		if err != nil {
			return '\x1b'
		}

		if seq[0] == '[' {
			switch seq[1] {
			case 'A':
				return ARROW_UP
			case 'B':
				return ARROW_DOWN
			case 'C':
				return ARROW_RIGHT
			case 'D':
				return ARROW_LEFT
			}
		}

		// We don't recognize this sequence, return <esc>
		return '\x1b'
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
	if buf[0] != '\x1b' || buf[1] != '[' {
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
// ================ Output ==================
// ==========================================

// editorRefreshScreen is called every cycle to repaint the screen.
func editorRefreshScreen() {
	// Hide the cursor before painting screen
	mainBuffer.WriteString("\x1b[?25l")
	// Reposition cursor to top left
	mainBuffer.WriteString("\x1b[H")

	editorDrawRows(&mainBuffer)

	// Draw cursor
	// +1 to put the cursor into terminal coordinates.
	fmt.Fprintf(&mainBuffer, "\x1b[%d;%dH", config.cy+1, config.cx+1)
	// Bring the cursor back
	mainBuffer.WriteString("\x1b[?25h")

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

// editorDrawRows draws the tilde column
func editorDrawRows(buf *strings.Builder) {
	for y := 0; y < config.screenrows; y++ {
		if y == config.screenrows/3 {
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
			buf.WriteString("~")
		}

		// Delete the rest of the line. This effectively clears
		// the screen when this function runs the first time.
		buf.WriteString("\x1b[K")

		if y < config.screenrows-1 {
			buf.WriteString("\r\n")
		}
	}
}

// ==========================================
// ================ Input ===================
// ==========================================

func editorMoveCursor(key int) {
	switch key {
	case ARROW_UP:
		config.cy--
	case ARROW_LEFT:
		config.cx--
	case ARROW_DOWN:
		config.cy++
	case ARROW_RIGHT:
		config.cx++
	}
}

func editorProcessKeypress() bool {
	char := editorReadKey()

	switch char {
	case CTRL_KEY('q'):
		// Quit
		cleanScreen(&mainBuffer)
		fmt.Print(mainBuffer.String())
		return false
	case ARROW_UP:
		fallthrough
	case ARROW_LEFT:
		fallthrough
	case ARROW_DOWN:
		fallthrough
	case ARROW_RIGHT:
		editorMoveCursor(char)
	}

	return true
}

// ==========================================
// ================= Main ===================
// ==========================================
func initializeEditor() {
	config.screenrows, config.screencols = getWindowSize()
	config.cx, config.cy = 0, 0
}
func main() {
	enableRawMode()
	defer exit()
	defer disableRawMode()
	initializeEditor()

	for {
		editorRefreshScreen()
		if !editorProcessKeypress() {
			break
		}
	}
}
