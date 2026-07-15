package modules

import "github.com/gin-gonic/gin"

type Registrar interface {
	Name() string
	Register(*gin.RouterGroup)
}

type module struct {
	name string
}

func (m module) Name() string {
	return m.name
}

func (m module) Register(parent *gin.RouterGroup) {
	parent.Group("/" + m.name)
}

func Foundation(overrides ...Registrar) []Registrar {
	byName := make(map[string]Registrar, len(overrides))
	for _, registrar := range overrides {
		byName[registrar.Name()] = registrar
	}

	names := []string{"identity", "configuration", "reception", "transcript", "matter", "knowledge", "record"}
	registrars := make([]Registrar, 0, len(names))
	for _, name := range names {
		if registrar, ok := byName[name]; ok {
			registrars = append(registrars, registrar)
			continue
		}
		registrars = append(registrars, module{name: name})
	}
	return registrars
}
