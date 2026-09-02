//go:build swaggerui

package server

import (
	httpswagger "github.com/swaggo/http-swagger/v2"
	"github.com/swaggo/swag"

	"github.com/taigrr/crush/internal/swagger"
)

func init() {
	swag.Register(swag.Name, &swag.Spec{
		InfoInstanceName: swag.Name,
		SwaggerTemplate:  string(swagger.JSON),
	})
	swaggerUIHandler = httpswagger.Handler(httpswagger.URL("swagger.json"))
}
