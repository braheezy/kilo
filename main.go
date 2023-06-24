package main

import (
	"bufio"
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

type editorConfig struct {
	originalTermios *unix.Termios
}

var config editorConfig

// ==========================================
// =============== Terminal =================
// ==========================================

// Thin wrapper around panic to gracefully exit.
func exit() {
	if r := recover(); r != nil {
		cleanScreen()
		log.Fatalf("%v\r\n", r)
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
		panic("Failed to get terminal settings: " + err.Error())
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
	raw.Cc[unix.VTIME] = 10

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

	defer exit()

	for {
		// Read a single character
		char, _, err = reader.ReadRune()
		if err != nil && err != io.EOF {
			panic("Failed to read character" + err.Error())
		}
		if char != '\u0000' {
			return char
		}
	}
}

// ==========================================
// ================ Output ==================
// ==========================================

// Clear the entire screen
// https://vt100.net/docs/vt100-ug/chapter3.html#ED
func cleanScreen() {
	// Wipe screen
	print("\x1b[2J")
	// Reposition cursor to top left
	print("\x1b[H")
}

func editorRefreshScreen() {
	cleanScreen()

	editorDrawRows()
	print("\x1b[H") // Reposition cursor to top left
}

func editorDrawRows() {
	for y := 0; y < 24; y++ {
		print("~\r\n")
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
// ================= Init ===================
// ==========================================

func main() {
	enableRawMode()
	defer disableRawMode()

	for {
		editorRefreshScreen()
		if !editorProcessKeypress() {
			break
		}
	}
}
