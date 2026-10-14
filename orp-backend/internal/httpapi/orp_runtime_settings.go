package httpapi

import "net.daoke/orp-backend/internal/store"

func (s *Server) runtimeValue(key string) string {
	if value, ok := s.runtimeSettings[key]; ok {
		return value.Value
	}
	for _, definition := range store.RuntimeSettings {
		if definition.Key == key {
			if definition.Sensitive {
				return ""
			}
			return definition.Value
		}
	}
	return ""
}

func (s *Server) runtimeSettingDefinition(key string) *store.RuntimeSetting {
	for index := range store.RuntimeSettings {
		if store.RuntimeSettings[index].Key == key {
			return &store.RuntimeSettings[index]
		}
	}
	return nil
}

func (s *Server) decorateRuntimeSetting(item orpDocument) {
	definition := s.runtimeSettingDefinition(toString(item["key"]))
	if definition == nil {
		return
	}
	item["runtimeManaged"] = true
	item["envKey"] = definition.EnvKey
	item["type"] = definition.Type
	item["isSensitive"] = definition.Sensitive
	item["restartRequired"] = definition.RestartRequired
	item["optional"] = definition.Optional
	source := "default"
	if effective, ok := s.runtimeSettings[definition.Key]; ok {
		source = effective.Source
	}
	item["effectiveSource"] = source
	if definition.Sensitive {
		item["value"] = "********"
	}
}
