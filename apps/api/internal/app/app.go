package app

import (
	"net/http"
	"time"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/config"
	identityhttp "github.com/1024XEngineer/xe6-tsy/apps/api/internal/httpapi/identity"
	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/httpserver"
	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules"
	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/provider/fake"
)

func New(cfg config.Config) http.Handler {
	var overrides []modules.Registrar
	if cfg.IdentityProvider == config.IdentityProviderMock {
		identityHandler := identityhttp.NewHandler(fake.NewIdentityService(time.Now))
		overrides = append(overrides, identityHandler)
	}
	return httpserver.New(cfg, modules.Foundation(overrides...))
}
