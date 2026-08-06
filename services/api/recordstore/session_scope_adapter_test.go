package recordstore

import "github.com/1024XEngineer/xe6-tsy/services/api/sessions"

var _ AccountSessionScopeReader = (*sessions.RecordsScopeReader)(nil)
