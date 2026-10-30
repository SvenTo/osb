// Command osb builds images and configures systems from a YAML configuration.
package main

import (
	"os"

	"github.com/SvenTo/osb/internal/cli"
)

var version = "dev"

func main() {
	cli.Version = version
	os.Exit(cli.Execute())
}
