package reception

import "github.com/gin-gonic/gin"

type Module struct {
	handler *Handler
	Store   *MemoryStore
}

func NewModule() *Module {
	store := NewMemoryStore()
	service := NewService(Deps{
		Store: store, Authorizer: &FakeAccessAuthorizer{},
		Configs: &FakeOrganizationConfigReader{}, Processing: DefaultFakeProcessingGate(),
		Media: NewFakeMediaAdapter(), Cleaner: &FakeMediaResourceCleaner{},
		Clock: SystemClock{}, IDs: &SequentialIDGenerator{},
	})
	return &Module{handler: NewHandler(service), Store: store}
}

func (m *Module) Name() string { return "reception" }

func (m *Module) Register(parent *gin.RouterGroup) {
	group := parent.Group("/reception")
	group.POST("/sessions", m.handler.Create)
	group.GET("/sessions/:session_id", m.handler.Get)
	group.POST("/sessions/:session_id/start", m.handler.Start)
	group.POST("/sessions/:session_id/media-tracks", m.handler.Attach)
	group.POST("/sessions/:session_id/media-tracks/:binding_id/detach", m.handler.Detach)
	group.POST("/sessions/:session_id/end", m.handler.End)
	group.POST("/sessions/:session_id/cancel", m.handler.Cancel)
}
