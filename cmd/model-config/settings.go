package main

import "github.com/vvvv11112222/cangjie/internal/modelsettings"

type pricing modelsettings.Pricing
type summary = modelsettings.Summary

func (p pricing) validate() error       { return modelsettings.Pricing(p).Validate() }
func money(value string) (int64, error) { return modelsettings.Money(value) }
func prepareSettings(demo map[string]string, p pricing) (map[string]string, error) {
	return modelsettings.PrepareSettings(demo, modelsettings.Pricing(p))
}
func checkSettings(settings map[string]string) (summary, error) {
	return modelsettings.CheckSettings(settings)
}
