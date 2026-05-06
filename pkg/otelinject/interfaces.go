// interfaces.go defines the core types and interfaces for the otelinject package.
// Language is a type alias for discovery.Language, kept for backward compatibility
// with mw-agent which imports otelinject.LanguageJava etc. OtelInjector defines
// the contract that each language-specific injector must implement.
package otelinject

import "github.com/middleware-labs/java-injector/pkg/discovery"

// Language is an alias for discovery.Language so that mw-agent can continue
// using otelinject.Language without breaking. Because this is a type alias (=),
// values are interchangeable with discovery.Language.
type Language = discovery.Language

const (
	LanguagePython Language = "python"
	LanguageJava   Language = "java"
	LanguageNode   Language = "node"
	LanguageGo     Language = "go"
	LanguageRust   Language = "rust"
	LanguagePHP    Language = "php"
	LanguageRuby   Language = "ruby"
)

// OtelInjector defines the contract for language-specific injectors. Each
// injector discovers processes of its language, validates required agent assets,
// and applies/removes instrumentation.
type OtelInjector interface {
	ValidateAssets(baseDir string) bool
	Instrument() error
	Uninstrument() error
	InstrumentService(discovery.ServiceSetting) error
}
