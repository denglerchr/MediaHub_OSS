package main

import (
	"embed"
	"io/fs"
	"log"
	"mediahub_oss/internal/cli"

	// Import docs for Swagger
	_ "mediahub_oss/docs"
)

//go:embed all:frontend_embed/browser
var frontendFS embed.FS

// @title SWCD MediaHub-API
// @version 3.3.0
// @description This is a server for a image, audio and file storage with integrated web-ui.
// @contact.name Christian Dengler
// @contact.url https://www.swcd.lu
// @contact.email denglerchr@gmail.com
// @servers.url /api
// @import encoding/json

// @securityDefinitions.basic BasicAuth

// @securityDefinitions.bearerauth BearerAuth
// @bearerFormat JWT / API Key
// @description Enter your JWT access token or API key ("srv_..."). Swagger UI automatically sends it with the "Bearer " prefix.

func main() {

	subFS, err := fs.Sub(frontendFS, "frontend_embed/browser")
	if err != nil {
		log.Fatalf("Failed to initialize frontend filesystem: %v", err)
	}

	// Delegate all execution to the CLI package
	cli.Execute(subFS)
}
