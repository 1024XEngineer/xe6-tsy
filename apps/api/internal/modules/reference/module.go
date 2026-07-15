package reference

import "github.com/gin-gonic/gin"

// Module is the Gin registration shell for module 6 reference capabilities. The
// route name is injected so the package can implement the design-doc reference
// boundary while the current seven-module router keeps the public "knowledge"
// path segment.
type Module struct {
	name string
	api  API
}

// NewModule creates a module 6 Gin registrar. A nil API is replaced with the
// skeleton service so backend startup does not depend on Handler, store, or RAG
// adapter completion.
func NewModule(name string, api API) Module {
	if api == nil {
		api = NewService(nil, nil, nil, nil, nil)
	}
	return Module{name: name, api: api}
}

// Name returns the route segment configured by the global module registry. This
// keeps compatibility shims explicit when package names and route names differ.
func (m Module) Name() string {
	return m.name
}

// Register attaches the module 6 route group to the versioned Gin parent. It
// intentionally registers no handlers yet, preserving the current 404 behavior
// until transport contracts are implemented.
func (m Module) Register(parent *gin.RouterGroup) {
	parent.Group("/" + m.Name())
}

// API exposes the module 6 application boundary for tests and future workflow
// orchestration without leaking the private service implementation.
func (m Module) API() API {
	return m.api
}
