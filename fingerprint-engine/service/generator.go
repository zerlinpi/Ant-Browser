package service

import "zerlinpi/Ant-Browser/fingerprint-engine/model"

// Generator creates fingerprint templates.
type Generator struct{}

func NewGenerator() *Generator {
	return &Generator{}
}

func (g *Generator) Generate(mode string) model.Template {
	return model.Template{
		ID: mode,
		Mode: mode,
		Browser: "chromium",
		Platform: "windows",
	}
}
