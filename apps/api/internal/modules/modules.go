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
	registrars := []Registrar{
		module{name: "identity"},
		module{name: "configuration"},
		module{name: "reception"},
		module{name: "transcript"},
		module{name: "matter"},
		module{name: "knowledge"},
		module{name: "record"},
	}
	for _, override := range overrides {
		for index := range registrars {
			if registrars[index].Name() == override.Name() {
				registrars[index] = override
				break
			}
		}
	}
	return registrars
}
