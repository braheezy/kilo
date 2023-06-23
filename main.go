package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"unicode"

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

var originalTermios *unix.Termios

// ==========================================
// =============== Terminal =================
// ==========================================

// enableRawMode turns on raw mode for the terminal. It remembers the settings of the terminal
// before the change so it can restore it later.
//
// Raw mode (as opposed to canonical mode) sends each input directly to program
// instead of buffering it and sending it when Enter is pressed.
func enableRawMode() {
	var err error
	originalTermios, err = unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS)
	if err != nil {
		panic("Failed to get terminal settings: " + err.Error())
	}
	raw := *originalTermios
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
	if err := unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, originalTermios); err != nil {
		panic("Failed to restore terminal settings: " + err.Error())
	}
}

// ==========================================
// ================= Init ===================
// ==========================================

func main() {
	enableRawMode()
	defer disableRawMode()

	// Point a Reader at STDIN
	reader := bufio.NewReader(os.Stdin)

	char := '\u0000'
	var err error

	for {
		// Read a single character
		char, _, err = reader.ReadRune()
		if err != nil {
			if err == io.EOF {
				break
			}
			panic("Failed to read character" + err.Error())
		}

		if char == 'q' {
			break
		}

		if unicode.IsControl(char) {
			fmt.Printf("%d\r\n", char)
		} else {
			fmt.Printf("%d ('%c')\r\n", char, char)
		}
	}
}
