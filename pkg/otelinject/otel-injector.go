package otelinject

import "github.com/middleware-labs/java-injector/pkg/discovery"

type Language string

const (
	LanguagePython Language = "python"
	LanguageJava   Language = "java"
	LanguageNode   Language = "node"
)

type OtelInjector interface {
	ValidateAssets(baseDir string) bool
	Instrument() error
	Uninstrument() error
	InstrumentService(discovery.ServiceSetting) error
}
