package egress

import "testing"

func TestSupportsScopePreservesPrimaryAndResourceCompatibility(t *testing.T) {
	tests := []struct {
		name               string
		nodeScope, request Scope
		want               bool
	}{
		{name: "exact Console asset", nodeScope: ScopeConsoleAsset, request: ScopeConsoleAsset, want: true},
		{name: "Console serves Console asset", nodeScope: ScopeConsole, request: ScopeConsoleAsset, want: true},
		{name: "Web serves Console asset", nodeScope: ScopeWeb, request: ScopeConsoleAsset, want: true},
		{name: "Web serves Web asset", nodeScope: ScopeWeb, request: ScopeWebAsset, want: true},
		{name: "Console asset does not serve primary Console", nodeScope: ScopeConsoleAsset, request: ScopeConsole, want: false},
		{name: "Web asset does not serve primary Web", nodeScope: ScopeWebAsset, request: ScopeWeb, want: false},
		{name: "Build remains isolated", nodeScope: ScopeBuild, request: ScopeConsoleAsset, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SupportsScope(test.nodeScope, test.request); got != test.want {
				t.Fatalf("SupportsScope(%q, %q) = %v, want %v", test.nodeScope, test.request, got, test.want)
			}
		})
	}
}

func TestInferNetworkKindFromName(t *testing.T) {
	cases := map[string]NetworkKind{
		"住宅Build":             NetworkKindResidential,
		"Residential US":      NetworkKindResidential,
		"移动 01":               NetworkKindMobile,
		"us-mobile-edge":      NetworkKindMobile,
		"mihomo:48010:香港 aws": NetworkKindDatacenter,
		"":                    NetworkKindDatacenter,
	}
	for name, want := range cases {
		if got := InferNetworkKind(name); got != want {
			t.Fatalf("InferNetworkKind(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestNormalizeNetworkKindAliases(t *testing.T) {
	if got := NormalizeNetworkKind("住宅"); got != NetworkKindResidential {
		t.Fatalf("住宅 = %q", got)
	}
	if got := NormalizeNetworkKind("机房"); got != NetworkKindDatacenter {
		t.Fatalf("机房 = %q", got)
	}
	if got := NormalizeNetworkKind("移动"); got != NetworkKindMobile {
		t.Fatalf("移动 = %q", got)
	}
	if got := NormalizeNetworkKind("unknown"); got != NetworkKindDatacenter {
		t.Fatalf("unknown = %q", got)
	}
}
