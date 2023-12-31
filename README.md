# kilo
A Go implementation of the [kilo](https://viewsourcecode.org/snaptoken/kilo/index.html) text editor.

Output of `kilo main.go`:

![](./kilo.png)

## Usage
Ensure [Go](https://go.dev/doc/install) is installed.

On Linux, build and run the code:

    make

## Thoughts
This was extremely beneficial, rather quick, and really fun to port the original C tutorial to Go. There were several instances where my implementation differs from the C implementation because of modern Go changes. For example, the C implementation uses static variables but those don't exist in Go so I used globals. The C implementation also does several things with pointers that would be considered unsafe today and Go requires more lines of code to safely do a similar task. Finally, Go has a drastically different approach to error handling.

When the final feature (multi-line comment syntax highlighting) was complete, here's what `make count` had to say:

    -------------------------------------------------------------------------------
    Language                     files          blank        comment           code
    -------------------------------------------------------------------------------
    Go                               1            163            269            960
    -------------------------------------------------------------------------------
    Total: 1392

Just about 1k SLOC as promised.
