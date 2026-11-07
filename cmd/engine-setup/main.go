// Command engine-setup is the installer's small, elevated service helper.
package main

import (
	"errors"
	"fmt"
	"os"

	"pbdeck/internal/svc"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		os.Exit(2)
	}
	if len(os.Args) == 3 && os.Args[2] == "--remove" {
		if err := svc.Uninstall(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := svc.Install(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var conflict *svc.OfficialConflict
		if errors.As(err, &conflict) {
			os.Exit(10)
		}
		os.Exit(1)
	}
}
