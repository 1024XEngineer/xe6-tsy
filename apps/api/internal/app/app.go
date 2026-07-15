package app

import (
	"net/http"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/config"
	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/httpserver"
	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules"
)

func New(cfg config.Config) http.Handler {
	return httpserver.New(cfg, modules.Foundation())
}
