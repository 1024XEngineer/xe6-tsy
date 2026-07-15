package reception

import "sync"

// Deps contains every capability required to construct a usable reception service.
type Deps struct {
	Store      TransactionalStore
	Authorizer AccessAuthorizer
	Configs    OrganizationConfigReader
	Processing ProcessingGate
	Media      MediaAdapter
	Cleaner    MediaResourceCleaner
	Clock      Clock
	IDs        IDGenerator
}

type Service struct {
	mu         sync.Mutex
	store      TransactionalStore
	authorizer AccessAuthorizer
	configs    OrganizationConfigReader
	processing ProcessingGate
	media      MediaAdapter
	cleaner    MediaResourceCleaner
	clock      Clock
	ids        IDGenerator
}

func NewService(deps Deps) *Service {
	return &Service{
		store: deps.Store, authorizer: deps.Authorizer, configs: deps.Configs,
		processing: deps.Processing, media: deps.Media, cleaner: deps.Cleaner,
		clock: deps.Clock, ids: deps.IDs,
	}
}
