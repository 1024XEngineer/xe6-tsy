package modules

import (
	"github.com/gin-gonic/gin"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules/matter"
	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules/reference"
)

// Registrar is the Gin-facing module boundary used by the global router. A
// registrar may expose only an empty route group while its application API is
// still under construction; Handler registration remains module-owned.
type Registrar interface {
	Name() string
	Register(*gin.RouterGroup)
}

// module is the placeholder registrar for modules whose application contract has
// not been introduced yet. It preserves the seven-module route shape without
// pretending a service implementation exists.
type module struct {
	name string
}

// Name returns the stable route segment for a placeholder module. Keeping this
// method on the registrar interface lets real modules and placeholders share the
// same Gin registration loop.
func (m module) Name() string {
	return m.name
}

// Register attaches an empty route group for modules that have no handlers yet.
// The empty group keeps startup and routing deterministic while preserving 404
// responses for unimplemented feature paths.
func (m module) Register(parent *gin.RouterGroup) {
	parent.Group("/" + m.name)
}

// Foundation returns the current module set in route order. Matter and reference
// already expose application APIs internally, while the remaining placeholders
// keep the public modular-monolith boundary stable until their contracts land.
func Foundation() []Registrar {
	return []Registrar{
		module{name: "identity"},
		module{name: "configuration"},
		module{name: "reception"},
		module{name: "transcript"},
		matter.NewModule(nil),
		reference.NewModule("knowledge", nil),
		module{name: "record"},
	}
}
