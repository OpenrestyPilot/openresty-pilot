package httpapi

import "testing"

func TestDemoTargetIsolation(t *testing.T) {
	for _, item := range []struct {
		name, endpoint, directory string
		port                      int
	}{
		{"openresty-east-1", "http://127.0.0.1:18081", "native-config", 18080},
		{"openresty-east-2", "http://127.0.0.1:18181", "native-config-east-2", 18180},
		{"openresty-east-3", "http://127.0.0.1:18281", "native-config-east-3", 18280},
	} {
		target, err := targetForNode(orpDocument{"name": item.name, "host": "127.0.0.1", "controlEndpoint": item.endpoint})
		if err != nil || target.directory != item.directory || target.healthPort != item.port {
			t.Fatalf("target %s: %+v, %v", item.name, target, err)
		}
		if _, err := targetForNode(orpDocument{"name": item.name, "host": "127.0.0.1", "controlEndpoint": "http://127.0.0.1:1"}); err == nil {
			t.Fatalf("target %s accepted a mismatched endpoint", item.name)
		}
	}
}

func TestDemoTargetHealthPortsAreUnique(t *testing.T) {
	nodes := []orpDocument{
		{"name": "openresty-east-1", "host": "127.0.0.1", "controlEndpoint": "http://127.0.0.1:18081"},
		{"name": "openresty-east-2", "host": "127.0.0.1", "controlEndpoint": "http://127.0.0.1:18181"},
		{"name": "openresty-east-3", "host": "127.0.0.1", "controlEndpoint": "http://127.0.0.1:18281"},
	}
	seen := map[int]bool{}
	for _, node := range nodes {
		target, err := targetForNode(node)
		if err != nil {
			t.Fatal(err)
		}
		if seen[target.healthPort] {
			t.Fatalf("demo health port %d is shared by multiple nodes", target.healthPort)
		}
		seen[target.healthPort] = true
	}
}
