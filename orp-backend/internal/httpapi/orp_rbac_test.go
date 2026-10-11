package httpapi

import "testing"

func TestMatchesRoleFilter(t *testing.T) {
	roleIDs := []int64{2, 7}
	tests := []struct {
		name   string
		filter string
		want   bool
	}{
		{name: "no filter", want: true},
		{name: "all sentinel", filter: "all", want: true},
		{name: "matching role", filter: "7", want: true},
		{name: "non matching role", filter: "9", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesRoleFilter(roleIDs, tt.filter); got != tt.want {
				t.Fatalf("matchesRoleFilter(%v, %q) = %v, want %v", roleIDs, tt.filter, got, tt.want)
			}
		})
	}
}

func TestAccountHasCenterScope(t *testing.T) {
	roles := []accountRole{{CenterIDs: []string{"12", "29"}}}
	if !accountHasCenterScope(roles, "12") || accountHasCenterScope(roles, "13") {
		t.Fatalf("center scope did not enforce assigned centers")
	}
	if !accountHasCenterScope([]accountRole{{CenterIDs: []string{"*"}}}, "any-center") {
		t.Fatalf("wildcard center scope should allow every center")
	}
	if accountHasCenterScope([]accountRole{{CenterIDs: []string{}}}, "12") {
		t.Fatalf("empty center scope must not grant center access")
	}
	if !accountHasCenterScope(roles, "") {
		t.Fatalf("centerless shared resources should remain accessible")
	}
}

func TestCenterIDString(t *testing.T) {
	if got := centerIDString(float64(42)); got != "42" {
		t.Fatalf("centerIDString(float64) = %q", got)
	}
	if got := centerIDString("legacy-center"); got != "legacy-center" {
		t.Fatalf("centerIDString(string) = %q", got)
	}
	if got := centerIDString(float64(42.5)); got != "" {
		t.Fatalf("non-integer center ID should be rejected, got %q", got)
	}
}

func TestAccountCenterIDs(t *testing.T) {
	roles := []accountRole{{Permissions: []string{"orp:publish"}, CenterIDs: []string{"12", "29"}}, {Permissions: []string{"orp:publish"}, CenterIDs: []string{"29", "41"}}}
	ids, all := accountCenterIDs(roles, "orp:publish")
	if all || len(ids) != 3 || ids[0] != "12" || ids[1] != "29" || ids[2] != "41" {
		t.Fatalf("accountCenterIDs() = (%v, %v), want ([12 29 41], false)", ids, all)
	}
	if ids, all = accountCenterIDs(append(roles, accountRole{Permissions: []string{"orp:publish"}, CenterIDs: []string{"*"}}), "orp:publish"); !all || ids != nil {
		t.Fatalf("wildcard role scope should represent all centers, got (%v, %v)", ids, all)
	}
}

func TestCenterScopeIsBoundToPermissionRole(t *testing.T) {
	roles := []accountRole{
		{Permissions: []string{"orp:config:write"}, CenterIDs: []string{"12"}},
		{Permissions: []string{"orp:read"}, CenterIDs: []string{"*"}},
	}
	if !accountHasCenterPermission(roles, "orp:config:write", "12") {
		t.Fatal("config permission should apply inside its assigned center")
	}
	if accountHasCenterPermission(roles, "orp:config:write", "29") {
		t.Fatal("read-only wildcard scope must not expand another role's config permission")
	}
	if !accountHasCenterPermission(roles, "orp:read", "29") {
		t.Fatal("read-only wildcard scope should allow reads across centers")
	}
}

func TestValidatedCenterIDs(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  []string
		valid bool
	}{
		{name: "selected centers", input: []any{"12", float64(29), "12"}, want: []string{"12", "29"}, valid: true},
		{name: "all centers", input: []any{"*"}, want: []string{"*"}, valid: true},
		{name: "wildcard mixed with center", input: []any{"*", "12"}, valid: false},
		{name: "wrong value type", input: []any{true}, valid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validatedCenterIDs(tt.input)
			if (err == nil) != tt.valid {
				t.Fatalf("validatedCenterIDs(%v) error = %v", tt.input, err)
			}
			if tt.valid && (len(got) != len(tt.want) || len(got) > 0 && got[0] != tt.want[0]) {
				t.Fatalf("validatedCenterIDs(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSensitivePermissionIsSeparate(t *testing.T) {
	regular := []accountRole{{Permissions: []string{"orp:read", "orp:audit:read"}}}
	if accountHasPermission(regular, "orp:secrets:read") {
		t.Fatal("audit read must not imply access to sensitive values")
	}
	secretReader := []accountRole{{Permissions: []string{"orp:read", "orp:audit:read", "orp:secrets:read"}}}
	if !accountHasPermission(secretReader, "orp:secrets:read") {
		t.Fatal("explicit sensitive data permission should be honored")
	}
}

func TestSensitivePermissionUsesItsOwnCenterScope(t *testing.T) {
	roles := []accountRole{
		{Permissions: []string{"orp:secrets:read"}, CenterIDs: []string{"12"}},
		{Permissions: []string{"orp:read"}, CenterIDs: []string{"*"}},
	}
	if !accountHasCenterPermission(roles, "orp:secrets:read", "12") {
		t.Fatal("sensitive permission should apply to its assigned center")
	}
	if accountHasCenterPermission(roles, "orp:secrets:read", "29") {
		t.Fatal("read scope from another role must not expand sensitive data access")
	}
}
