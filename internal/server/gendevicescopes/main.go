// Command gendevicescopes writes the device route → scope table
// (internal/server/devicescopes_gen.go) from the x-crush-scope
// annotations in internal/swagger. Run `task swag` first.
package main

import (
	"fmt"
	"go/format"
	"os"

	"github.com/taigrr/crush/internal/server/devicescope"
	"github.com/taigrr/crush/internal/swagger"
)

const output = "devicescopes_gen.go"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gendevicescopes:", err)
		os.Exit(1)
	}
}

func run() error {
	routes, err := devicescope.FromSwagger([]byte(swagger.SwaggerInfo.ReadDoc()))
	if err != nil {
		return err
	}
	src, err := format.Source(devicescope.Source("server", routes))
	if err != nil {
		return err
	}
	return os.WriteFile(output, src, 0o644)
}
