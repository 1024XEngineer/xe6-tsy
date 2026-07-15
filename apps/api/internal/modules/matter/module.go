package matter

import "github.com/gin-gonic/gin"

// Module is the Gin registration shell for module 5. It owns the HTTP path
// boundary while keeping actual application behavior behind API so future
// handlers can be added without changing the global router contract.
type Module struct {
	api API
}

// NewModule creates the module 5 Gin registrar. A nil API is replaced with the
// skeleton service so the backend remains startable before Handler and storage
// work is implemented.
func NewModule(api API) Module {
	if api == nil {
		api = NewService(nil, nil)
	}
	return Module{api: api}
}

// Name returns the stable route segment for module 5. Keeping this value here
// avoids duplicating Gin path strings in the global module registry.
func (m Module) Name() string {
	return "matter"
}

// Register attaches the module 5 route group to the versioned Gin parent. It
// intentionally registers no handlers yet, preserving the current 404 behavior
// until transport contracts are implemented.
func (m Module) Register(parent *gin.RouterGroup) {
	parent.Group("/" + m.Name())
}

// API exposes the module 5 application boundary for tests and future internal
// orchestration. Callers should use this interface rather than reaching into the
// private service implementation.
func (m Module) API() API {
	return m.api
}
