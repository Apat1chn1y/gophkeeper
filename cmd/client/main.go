// Command client is the GophKeeper command-line client.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"gophkeeper/internal/client/cli"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is main's testable core: it returns a process exit code instead
// of calling os.Exit directly.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if err := cli.Run(context.Background(), args, stdin, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}
