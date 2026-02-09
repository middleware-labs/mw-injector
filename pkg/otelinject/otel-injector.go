package otelinject

type OtelInjector interface {
	ValidateAssets(baseDir string) bool
	Instrument() error
}
