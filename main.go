package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"golang.org/x/sys/unix"
)

// ==========================================
// =============== Defines* =================
// ==========================================

// CTRL_KEY is a mask for the control keys,
// stripping bits 5 and 6 from the character code, k.
func CTRL_KEY(k rune) rune {
	return k & 0x1f
}

// ==========================================
// ================= Data ===================
// ==========================================

// Maintain state of the editor.
type editorConfig struct {
	originalTermios *unix.Termios
	screenrows      int
	screencols      int
}

var config editorConfig

// ==========================================
// =============== Terminal =================
// ==========================================

// Thin wrapper around panic to gracefully exit.
func exit() {
	if r := recover(); r != nil {
		cleanScreen()
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
func editorReadKey() (char rune) {
	var err error
	// Point a Reader at STDIN
	reader := bufio.NewReader(os.Stdin)

	for {
		// Read a single character
		char, _, err = reader.ReadRune()
		if err != nil && err != io.EOF {
			panic("Failed to read character from terminal: " + err.Error())
		}
		if char != '\u0000' {
			return char
		}
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

// Clear the entire screen
// https://vt100.net/docs/vt100-ug/chapter3.html#ED
func cleanScreen() {
	// Wipe screen
	fmt.Print("\x1b[2J")
	// Reposition cursor to top left
	fmt.Print("\x1b[H")
}

func editorRefreshScreen() {
	cleanScreen()

	editorDrawRows()
	fmt.Print("\x1b[H") // Reposition cursor to top left
}

// editorDrawRows draws the tilde column
func editorDrawRows() {
	for y := 0; y < config.screenrows; y++ {
		fmt.Print("~\r\n")
	}
}

// ==========================================
// ================ Input ===================
// ==========================================
func editorProcessKeypress() bool {
	char := editorReadKey()

	switch char {
	case CTRL_KEY('q'):
		// Quit
		cleanScreen()
		return false
	}

	return true
}

// ==========================================
// ================= Main ===================
// ==========================================
func main() {
	// TODO: If this stuff is in an init() function, things don't work. Why not?
	enableRawMode()
	defer exit()
	defer disableRawMode()
	config.screenrows, config.screencols = getWindowSize()

	for {
		editorRefreshScreen()
		if !editorProcessKeypress() {
			break
		}
	}
}
