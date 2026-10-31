package main

import (
	"github.com/Avicted/tools.pipeline-graph/internal/ci"
	"github.com/Avicted/tools.pipeline-graph/internal/ci/github"
	"github.com/Avicted/tools.pipeline-graph/internal/ci/teamcity"
	"github.com/Avicted/tools.pipeline-graph/internal/config"
)

// providers are the CI/CD systems that can be read; add new ones here.
var providers = []ci.Provider{teamcity.New(), github.New()}

// settingsBlocks are the names of the providers that take settings, which are the
// keys a config may have besides its own fields.
func settingsBlocks() []string {
	var names []string
	for _, p := range providers {
		if _, ok := p.(ci.Configurable); ok {
			names = append(names, p.System().Name)
		}
	}
	return names
}

// providerSettings decodes every configurable provider's settings for repo i.
func providerSettings(cfg *config.Config, i int) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range providers {
		if c, ok := p.(ci.Configurable); ok {
			v := c.NewSettings()
			if err := cfg.Settings(i, p.System().Name, v); err != nil {
				return nil, err
			}
			out[p.System().Name] = v
		}
	}
	return out, nil
}
