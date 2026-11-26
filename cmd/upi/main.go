package main

import (
	"os"

	_ "github.com/MichaelKinsy/PiG/ai/all"
	"github.com/MichaelKinsy/PiG/coding/cli"
	_ "github.com/MichaelKinsy/PiG/coding/extension/host/lispcell" // builds Lisp extensions, which an embedding program of the extension host does not
)

func main() {
	cli.Main(os.Args[1:], nil)
}
